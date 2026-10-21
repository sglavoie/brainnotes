package vault

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// observeVacancies reports every vacancy a presence watcher records for the
// rest of the test. A test must not call t.Parallel while using it: the hook is
// package state. Sends never block the watcher; a dropped report is repeated on
// the watcher's next pass for as long as the path stays missing.
func observeVacancies(t *testing.T) <-chan string {
	t.Helper()
	reports := make(chan string, 1)
	previous := presenceVacancyObserved
	presenceVacancyObserved = func(path string) {
		select {
		case reports <- path:
		default:
		}
	}
	t.Cleanup(func() { presenceVacancyObserved = previous })
	return reports
}

// awaitVacancy blocks until a watcher has recorded path as missing. The
// deadline is only a guard against a hung test; a passing run never waits on
// anything but the watcher.
func awaitVacancy(reports <-chan string, path string) error {
	deadline := time.After(30 * time.Second)
	for {
		select {
		case got := <-reports:
			if got == path {
				return nil
			}
		case <-deadline:
			return fmt.Errorf("no watcher reported %s missing", path)
		}
	}
}

// resetProvenVolumes drops the per-process capability cache around a test, so
// one test's verdict cannot decide another's and the self-test itself is
// observable.
func resetProvenVolumes(t *testing.T) {
	t.Helper()
	forgetProvenVolumesForTest()
	t.Cleanup(forgetProvenVolumesForTest)
}

func TestProveReplacementAcceptsTheProductionPrimitive(t *testing.T) {
	if !nativeReplacementSupported {
		t.Skipf("no path-preserving replacement primitive on %s", runtime.GOOS)
	}
	dir := t.TempDir()
	if err := proveReplacement(dir, nativeReplace); err != nil {
		t.Fatalf("the primitive this package ships must pass its own self-test: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("the self-test must clean up after itself, found %v", names)
	}
}

// The whole point of running the primitive instead of trusting the build tags is
// to catch a volume whose behavior differs from its syscall's contract. Each
// fake below is a way that can happen on a real filesystem.
func TestProveReplacementRefusesMisbehavingPrimitives(t *testing.T) {
	vacancies := observeVacancies(t)
	cases := []struct {
		name    string
		wants   string
		replace func(source, destination string) (string, error)
	}{
		{
			name:    "one that vacates the destination",
			wants:   "absent part-way through",
			replace: vacatingReplace(vacancies),
		},
		{
			name:    "one that does not deliver the replacement",
			wants:   "did not take the replacement",
			replace: undeliveredReplace,
		},
		{
			name:  "one that loses the displaced content",
			wants: "displaced content was not preserved",
			// A copy rather than an exchange: the destination ends up correct,
			// so only inspecting it would pass, but what it held is gone.
			replace: copyingReplace,
		},
		{
			name:  "one the kernel rejects outright",
			wants: "failed on this volume",
			replace: func(source, destination string) (string, error) {
				return "", errors.New("operation not supported")
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			dir := t.TempDir()
			err := proveReplacement(dir, testCase.replace)
			assertReplacementRefusal(t, err, dir, testCase.wants)
		})
	}
}

func assertReplacementRefusal(t *testing.T, err error, dir, wants string) {
	t.Helper()
	if err == nil {
		t.Fatal("the self-test must refuse this volume")
	}
	if !IsUnsafeReplacement(err) {
		t.Fatalf("refusal must be an *UnsafeReplacementError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), wants) {
		t.Fatalf("refusal should say why (%q), got %q", wants, err.Error())
	}
	if !strings.Contains(err.Error(), dir) {
		t.Fatalf("refusal must name what was refused, got %q", err.Error())
	}
}

// vacatingReplace moves the destination away before delivering the source.
func vacatingReplace(vacancies <-chan string) func(source, destination string) (string, error) {
	return func(source, destination string) (string, error) {
		displaced := destination + ".away"
		if err := os.Rename(destination, displaced); err != nil {
			return "", err
		}
		// The gap a synchronizing file provider reads as a deletion,
		// held open until the watcher has seen it.
		if err := awaitVacancy(vacancies, destination); err != nil {
			return "", err
		}
		if err := os.Rename(source, destination); err != nil {
			return "", err
		}
		return displaced, nil
	}
}

func undeliveredReplace(source, destination string) (string, error) {
	displaced := destination + ".away"
	if err := os.WriteFile(displaced, []byte("something else\n"), 0o600); err != nil {
		return "", err
	}
	return displaced, nil
}

func copyingReplace(source, destination string) (string, error) {
	data, err := os.ReadFile(source)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(destination, data, replacementProbeMode); err != nil {
		return "", err
	}
	if err := os.Chmod(destination, replacementProbeMode); err != nil {
		return "", err
	}
	return source, nil
}

// A volume that failed the self-test may be a different filesystem by the next
// call — a mount changing under the same path is exactly the case worth
// re-checking — so only a success may be remembered.
func TestRequireProvenReplacementCachesOnlySuccess(t *testing.T) {
	resetProvenVolumes(t)
	dir := t.TempDir()

	original := proveVolume
	t.Cleanup(func() { proveVolume = original })

	runs := 0
	refusal := &UnsafeReplacementError{Path: dir, Reason: "the mount was busy"}
	proveVolume = func(string) error {
		runs++
		return refusal
	}
	for i := 0; i < 3; i++ {
		if err := requireProvenReplacement(dir); !errors.Is(err, error(refusal)) {
			t.Fatalf("attempt %d = %v, want the refusal", i, err)
		}
	}
	if runs != 3 {
		t.Fatalf("a failed self-test ran %d times, want one run per attempt", runs)
	}

	proveVolume = func(string) error {
		runs++
		return nil
	}
	for i := 0; i < 3; i++ {
		if err := requireProvenReplacement(dir); err != nil {
			t.Fatalf("attempt %d after success = %v", i, err)
		}
	}
	if runs != 4 {
		t.Fatalf("self-test ran %d times, want the success to be cached after one run", runs)
	}
}

func TestReplaceExistingRefusesCrossVolumeSources(t *testing.T) {
	resetProvenVolumes(t)
	dir := t.TempDir()
	other := otherVolumeDir(t)

	destination := filepath.Join(dir, "note.md")
	if err := os.WriteFile(destination, []byte("destination\n"), 0o644); err != nil {
		t.Fatalf("write destination: %v", err)
	}
	source := filepath.Join(other, "staged.md")
	if err := os.WriteFile(source, []byte("staged\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(source) })

	displaced, err := replaceExisting(source, destination)
	if err == nil {
		t.Fatalf("a cross-volume replacement must be refused, displaced = %q", displaced)
	}
	if !IsUnsafeReplacement(err) {
		t.Fatalf("refusal must be an *UnsafeReplacementError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "crosses volumes") {
		t.Fatalf("refusal should name the condition, got %q", err.Error())
	}
	if got := readFileString(t, destination); got != "destination\n" {
		t.Fatalf("a refused replacement must not touch the destination, got %q", got)
	}
}

// A machine with one volume still has to prove it refuses the cross-volume
// case, because the refusal is what turns a confusing EXDEV deep inside a
// commit into a named condition before anything is touched.
func TestReplaceExistingRefusesASimulatedSecondVolume(t *testing.T) {
	resetProvenVolumes(t)
	dir := t.TempDir()
	staging := filepath.Join(dir, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	destination := filepath.Join(dir, "note.md")
	if err := os.WriteFile(destination, []byte("destination\n"), 0o644); err != nil {
		t.Fatalf("write destination: %v", err)
	}
	source := filepath.Join(staging, "staged.md")
	if err := os.WriteFile(source, []byte("staged\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	original := volumeIdentity
	t.Cleanup(func() { volumeIdentity = original })
	volumeIdentity = func(probe string) (string, error) {
		if probe == staging {
			return "volume:other", nil
		}
		return original(probe)
	}

	proven := 0
	originalProve := proveVolume
	t.Cleanup(func() { proveVolume = originalProve })
	proveVolume = func(probe string) error {
		proven++
		return originalProve(probe)
	}

	if _, err := replaceExisting(source, destination); !IsUnsafeReplacement(err) {
		t.Fatalf("a cross-volume replacement must be refused as unsafe, got %v", err)
	} else if !strings.Contains(err.Error(), "crosses volumes") {
		t.Fatalf("refusal should name the condition, got %q", err.Error())
	}
	if proven != 0 {
		t.Fatal("the volume check must come first: nothing should be probed on a doomed call")
	}
	if got := readFileString(t, destination); got != "destination\n" {
		t.Fatalf("a refused replacement must not touch the destination, got %q", got)
	}
	if got := readFileString(t, source); got != "staged\n" {
		t.Fatalf("a refused replacement must not touch the source, got %q", got)
	}
}

// otherVolumeDir finds a writable directory on a different volume, or skips.
// There is no portable way to make one, so the test uses whatever the machine
// happens to have and stays honest about not running when there is nothing.
func otherVolumeDir(t *testing.T) string {
	t.Helper()
	here, err := volumeIdentity(t.TempDir())
	if err != nil {
		t.Skipf("volume identity unavailable: %v", err)
	}
	candidates := []string{"/dev/shm", "/run/user/" + fmt.Sprint(os.Getuid()), os.TempDir(), "/tmp", "/private/tmp"}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, home)
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err != nil || !info.IsDir() {
			continue
		}
		volume, err := volumeIdentity(candidate)
		if err != nil || volume == here {
			continue
		}
		probe, err := os.MkdirTemp(candidate, "bn-volume-")
		if err != nil {
			continue
		}
		t.Cleanup(func() { _ = os.RemoveAll(probe) })
		return probe
	}
	t.Skip("no second writable volume on this machine")
	return ""
}

// replaceExisting is the one call an overwrite makes, so its own continuity is
// asserted directly rather than only through a transaction.
func TestReplaceExistingKeepsTheDestinationPresentAndReturnsWhatItDisplaced(t *testing.T) {
	if !nativeReplacementSupported {
		t.Skipf("no path-preserving replacement primitive on %s", runtime.GOOS)
	}
	resetProvenVolumes(t)
	dir := t.TempDir()
	destination := filepath.Join(dir, "note.md")
	if err := os.WriteFile(destination, []byte("before\n"), 0o644); err != nil {
		t.Fatalf("write destination: %v", err)
	}
	source, err := stageTempFile(destination, []byte("after\n"), 0o644)
	if err != nil {
		t.Fatalf("stage: %v", err)
	}

	watch := watchPresence([]string{destination})
	displaced, err := replaceExisting(source, destination)
	vanished := watch.stop()
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if len(vanished) > 0 {
		t.Fatalf("the destination must never stop existing, saw it absent: %v", vanished)
	}
	if got := readFileString(t, destination); got != "after\n" {
		t.Fatalf("destination = %q, want after", got)
	}
	if got := readFileString(t, displaced); got != "before\n" {
		t.Fatalf("displaced = %q, want before", got)
	}
	if err := os.Remove(displaced); err != nil {
		t.Fatalf("remove displaced: %v", err)
	}
}

func TestPresenceWatchSeesAVacancyAndOnlyAVacancy(t *testing.T) {
	dir := t.TempDir()
	continuous := filepath.Join(dir, "continuous")
	vacated := filepath.Join(dir, "vacated")
	for _, path := range []string{continuous, vacated} {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	vacancies := observeVacancies(t)
	watch := watchPresence([]string{continuous, vacated})
	if err := os.Rename(vacated, vacated+".away"); err != nil {
		t.Fatalf("rename away: %v", err)
	}
	if err := awaitVacancy(vacancies, vacated); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(vacated+".away", vacated); err != nil {
		t.Fatalf("rename back: %v", err)
	}
	vanished := watch.stop()

	if len(vanished) != 1 || vanished[0] != "vacated" {
		t.Fatalf("vanished = %v, want exactly [vacated]", vanished)
	}
}

func TestRemoveSupersededReportsALeftoverTempFile(t *testing.T) {
	dir := t.TempDir()
	destination := filepath.Join(dir, "note.md")
	leftover := filepath.Join(dir, ".txn-gone.tmp")

	err := removeSuperseded(destination, leftover)
	if err == nil {
		t.Fatal("removing a file that is not there must be reported")
	}
	if !IsTxnCleanupFailure(err) {
		t.Fatalf("a housekeeping failure must be typed as one, got %T: %v", err, err)
	}
	var cleanup *TxnCleanupError
	if !errors.As(err, &cleanup) {
		t.Fatalf("errors.As: %v", err)
	}
	if cleanup.Path != destination || cleanup.LeftoverPath != leftover {
		t.Fatalf("cleanup = %+v, want the destination and the leftover named", cleanup)
	}
	if !strings.Contains(err.Error(), destination) || !strings.Contains(err.Error(), leftover) {
		t.Fatalf("the message must name both paths, got %q", err.Error())
	}
	if IsTxnConflict(err) || IsTxnRecoveryNeeded(err) {
		t.Fatalf("a leftover temp file is not a failed mutation: %v", err)
	}
}
