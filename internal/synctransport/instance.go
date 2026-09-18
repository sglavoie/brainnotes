package synctransport

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// shutdownGrace is how long a device gets to exit on its own before it is
// killed. Syncthing closes its database on the way out and a database left
// unclean makes the *next* start slow, which a restart test would see as a
// flake rather than as the cleanup being impatient.
const shutdownGrace = 20 * time.Second

// process is one running child and the two things needed to end it: where its
// output went, and a way to learn it has exited without blocking on it.
type process struct {
	cmd  *exec.Cmd
	log  *os.File
	done chan error
}

// start launches the device and blocks until its administration surface answers.
//
// Waiting for the API rather than for the process is what makes the rest of the
// harness safe to write imperatively: once this returns, the configuration has
// been read, the folders exist, and a pause or a status query will be answered
// rather than refused by a listener that is not up yet.
func (d *Device) start() {
	d.t.Helper()
	if d.proc != nil {
		d.t.Fatalf("%s is already running", d.Label)
	}
	log := d.openLog()
	cmd := exec.Command(d.binary, "serve", "--home", d.home, "--no-browser")
	cmd.Env = d.env()
	cmd.Stdout = log
	cmd.Stderr = log
	if err := cmd.Start(); err != nil {
		_ = log.Close()
		d.t.Fatalf("%s: start syncthing: %v", d.Label, err)
	}
	// Closed after the result is sent, so that the exit can be observed more than
	// once. awaitReady *consumes* the value before failing, and the cleanup
	// registered below then waits on the same channel: without the close, a
	// process that died before it was ready would leave Stop blocked on a value
	// nobody will ever send again, and the gate would hang at its own timeout
	// instead of printing the reason the child exited.
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
		close(done)
	}()
	d.proc = &process{cmd: cmd, log: log, done: done}
	// Registered here rather than once the cluster is up, because everything
	// between this line and a connected cluster fails by calling t.Fatalf: a
	// configuration this release refuses, an administration port something else
	// took between reserving it and binding it, a peer that never answers. Each of
	// those ends the test with this process running, and the test framework is
	// about to delete the directories underneath it. A cleanup registered later
	// covers only the runs that did not need it.
	d.t.Cleanup(d.Stop)
	d.awaitReady()
}

// openLog appends, so that the output of a restarted device joins the output of
// the one it replaced and a failure message shows the whole run.
//
//bn:direct-write the log lives in this device's Syncthing home, which refuseUnlessDisposable refused unless it is throwaway fixture storage
func (d *Device) openLog() *os.File {
	d.t.Helper()
	log, err := os.OpenFile(d.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		d.t.Fatalf("%s: open log: %v", d.Label, err)
	}
	return log
}

// awaitReady polls the private API until it answers, and gives up early if the
// process has already died — otherwise a configuration this release refuses
// shows up as a timeout instead of as the error message it printed.
func (d *Device) awaitReady() {
	d.t.Helper()
	deadline := time.Now().Add(awaitTimeout)
	for {
		select {
		case err := <-d.proc.done:
			d.t.Fatalf("%s exited before it was ready (%v)\n%s", d.Label, err, d.logTail())
		default:
		}
		if err := d.ping(); err == nil {
			return
		}
		if time.Now().After(deadline) {
			d.t.Fatalf("%s did not answer on %s within %s\n%s", d.Label, d.admin, awaitTimeout, d.logTail())
		}
		time.Sleep(awaitPoll)
	}
}

// Stop ends the device and waits for it, so that a caller who stops one can
// state what the other one sees without racing a process that is still closing
// connections. Stopping an already-stopped device does nothing.
func (d *Device) Stop() {
	d.t.Helper()
	if d.proc == nil {
		return
	}
	proc := d.proc
	d.proc = nil
	if err := proc.cmd.Process.Signal(os.Interrupt); err != nil {
		_ = proc.cmd.Process.Kill()
	}
	select {
	case <-proc.done:
	case <-time.After(shutdownGrace):
		_ = proc.cmd.Process.Kill()
		<-proc.done
	}
	_ = proc.log.Close()
}

// Restart stops the device and starts it again on the same home, the same
// identity, the same ports and the same configuration.
//
// Reusing the home is the entire point. A restart that generated a new identity
// would be a new device joining a cluster, which is a different scenario and a
// much weaker one: what a real deployment does is lose power and come back, and
// the claim under test is that the transport resumes from its database rather
// than from nothing.
func (d *Device) Restart() {
	d.t.Helper()
	d.Stop()
	d.start()
}

// Running reports whether this device has a live process, which is how a test
// asserts that a scenario stopped what it meant to stop.
func (d *Device) Running() bool { return d.proc != nil }

// run invokes the binary for a one-shot subcommand.
func (d *Device) run(args ...string) (string, error) {
	cmd := exec.Command(d.binary, args...)
	cmd.Env = d.env()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// env builds the child's environment rather than inheriting one.
//
// internal/vaultfixture already rewrote HOME and the XDG directories for this
// process, and inheriting that would be *almost* right; building the list means
// the child's view is stated here instead of being the residue of whatever the
// test process was started with. HOME points at the device's own Syncthing home
// so that nothing this program reads can be shared between the two devices.
func (d *Device) env() []string {
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + d.home,
		// Syncthing restarts itself in place on some errors, replaces itself on
		// an upgrade, and creates a "Default Folder" pointing at ~/Sync on a
		// first run. All three would take the process somewhere this harness did
		// not put it.
		"STNORESTART=1",
		"STNOUPGRADE=1",
		"STNODEFAULTFOLDER=1",
	}
	if tmp := os.Getenv("TMPDIR"); tmp != "" {
		env = append(env, "TMPDIR="+tmp)
	}
	return env
}

// logLines is how much of a device's output a failure message carries. Enough to
// see the last connection or folder event, short enough that two of them do not
// bury the assertion that failed.
const logLines = 40

// logTail returns the end of this device's output, labelled.
func (d *Device) logTail() string {
	body, err := os.ReadFile(d.logPath)
	if err != nil {
		return fmt.Sprintf("--- %s log unavailable: %v", d.Label, err)
	}
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	if len(lines) > logLines {
		lines = lines[len(lines)-logLines:]
	}
	return fmt.Sprintf("--- %s (%s) ---\n%s", d.Label, d.home, strings.Join(lines, "\n"))
}
