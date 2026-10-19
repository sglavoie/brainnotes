package cli

// ChildProcessError marks a failure of a program the user asked bn to run on
// their behalf — the `bn checkpoint run` child, the editor, fzf — whose exit
// status, or whose "not found" / "cannot execute", becomes bn's own (cmd/bn).
//
// Only these are mapped. A git or npm subprocess bn runs for its own purposes
// also fails as an *exec.ExitError, but its status means nothing to the
// caller, so it stays an ordinary error and exits 2: a `bn set` whose Git
// preflight hit git's 128 must not exit 128.
type ChildProcessError struct {
	Err error
}

func (e *ChildProcessError) Error() string { return e.Err.Error() }
func (e *ChildProcessError) Unwrap() error { return e.Err }

// childProcessError wraps err as a user-visible child's failure; nil stays nil.
func childProcessError(err error) error {
	if err == nil {
		return nil
	}
	return &ChildProcessError{Err: err}
}
