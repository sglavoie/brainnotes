package web

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// noteLoadTimeout bounds a single note read so one stalled file cannot block the
// vault walk — and, through it, an HTTP handler — indefinitely.
const noteLoadTimeout = 2 * time.Second

// noteLoader loads a single note. It exists so tests can inject a blocking or
// failing loader in place of (*vault.Note).Load; production always uses Load.
type noteLoader func(*vault.Note) error

// errNoteLoadTimeout marks a note whose read exceeded the bounded load timeout.
// Timed-out notes are quarantined for the life of the process and reported as
// skipped rather than folded into frontmatter parse errors.
var errNoteLoadTimeout = errors.New("web: note load timed out")

// loadedVault is the classified result of a bounded vault walk: healthy notes,
// frontmatter/YAML parse errors (unchanged classification), notes skipped
// because their read timed out or they were already quarantined, and the
// absolute paths that newly timed out and must be quarantined.
type loadedVault struct {
	loaded      []diagnosticLoadedNote
	parseErrors []diagnosticFileError
	skipped     []diagnosticFileError
	timedOut    []string
}

// loadVaultNotes walks the vault and loads every note under a per-note time
// bound, keeping healthy notes even when some reads stall or fail. Notes whose
// absolute path is in quarantined are skipped without any read attempt. A read
// that exceeds timeout is classified as skipped and its path is returned in
// timedOut so the caller can quarantine it; any other load error keeps the
// existing frontmatter parse-error classification. A nil load defaults to
// (*vault.Note).Load and a non-positive timeout defaults to noteLoadTimeout.
func loadVaultNotes(v *vault.Vault, load noteLoader, timeout time.Duration, quarantined map[string]struct{}) (loadedVault, error) {
	if v == nil {
		return loadedVault{}, errorsf("nil vault")
	}
	if load == nil {
		load = (*vault.Note).Load
	}
	if timeout <= 0 {
		timeout = noteLoadTimeout
	}

	var result loadedVault
	err := v.Walk(vault.WalkFilter{}, func(note *vault.Note) error {
		rel := displayPath(note.Path, v.Root)
		if _, quarantined := quarantined[note.Path]; quarantined {
			result.skipped = append(result.skipped, diagnosticFileError{
				Path:  rel,
				Error: errNoteLoadTimeout.Error(),
			})
			return nil
		}
		if err := boundedLoad(load, note, timeout); err != nil {
			if errors.Is(err, errNoteLoadTimeout) {
				result.skipped = append(result.skipped, diagnosticFileError{
					Path:  rel,
					Error: err.Error(),
				})
				result.timedOut = append(result.timedOut, note.Path)
				return nil
			}
			result.parseErrors = append(result.parseErrors, diagnosticFileError{
				Path:  rel,
				Error: err.Error(),
			})
			return nil
		}
		result.loaded = append(result.loaded, diagnosticLoadedNote{note: note, rel: rel})
		return nil
	})
	if err != nil {
		return loadedVault{}, fmt.Errorf("web: walk notes: %w", err)
	}

	sortDiagnosticFileErrors(result.parseErrors)
	sortDiagnosticFileErrors(result.skipped)
	sort.Strings(result.timedOut)
	return result, nil
}

// boundedLoad runs load(note) in a goroutine and returns errNoteLoadTimeout if
// it does not finish within timeout. The result channel is buffered so the
// goroutine can always deliver its result and exit even after a timeout has
// been returned; a read that never unblocks leaks only that one goroutine
// rather than stalling the caller.
func boundedLoad(load noteLoader, note *vault.Note, timeout time.Duration) error {
	done := make(chan error, 1)
	go func() {
		done <- load(note)
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		return errNoteLoadTimeout
	}
}

func sortDiagnosticFileErrors(errs []diagnosticFileError) {
	sort.Slice(errs, func(i, j int) bool {
		return errs[i].Path < errs[j].Path
	})
}
