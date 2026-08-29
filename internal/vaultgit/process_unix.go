//go:build unix

package vaultgit

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// configureCommandCancellation puts git and every process it spawns in a
// dedicated process group so cancellation kills the whole tree.
//
// It matters for exactly one command here, and it matters a lot: `git push`
// over SSH forks an ssh child that holds the inherited stderr pipe. Killing
// only git leaves that ssh alive, blocked on a network the LaunchAgent has no
// reason to believe will ever answer, with a pipe that keeps Wait from
// returning. This mirrors internal/agent's runner for the same reason.
func configureCommandCancellation(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
