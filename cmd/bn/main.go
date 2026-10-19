// Command bn is the entrypoint for Brain Notes, a CLI for plain-markdown vaults.
package main

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"syscall"

	"github.com/sglavoie/sglavoie_brainnotes/internal/cli"
)

var execute = cli.Execute

func main() {
	os.Exit(run(os.Stderr))
}

func run(stderr io.Writer) int {
	err := execute()
	if err == nil {
		return 0
	}
	if isBrokenPipe(err) {
		return 0
	}
	if errors.Is(err, context.Canceled) {
		return 130
	}
	if errors.Is(err, cli.ErrFZFInterrupted) {
		return 130
	}
	cli.RenderError(stderr, err)
	return exitCode(err)
}

func exitCode(err error) int {
	if code, ok := processExitCode(err); ok {
		return code
	}
	// grep's convention: 1 is a completed run with a negative answer (lint
	// violations, nothing selected, a declined prompt), 2 is everything that
	// went wrong, from an unknown flag to a vault that does not load.
	if cli.IsNegativeOutcome(err) {
		return 1
	}
	return 2
}

// processExitCode maps a failure to run or finish an external program to the
// shell's own codes: the child's status, 126 for "cannot execute", 127 for
// "not found", 128+N for a signal.
//
// Only a child the user asked for — the `bn checkpoint run` command, the
// editor, fzf — is mapped, and cli marks those with *cli.ChildProcessError.
// A git subprocess bn runs for its own bookkeeping fails as an *exec.ExitError
// too, but its 128 would read as "killed by a signal", so it is an ordinary
// error (exit 2).
func processExitCode(err error) (int, bool) {
	var child *cli.ChildProcessError
	if !errors.As(err, &child) {
		return 0, false
	}
	err = child.Err

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if code := exitErr.ExitCode(); code >= 0 {
			return code, true
		}
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return 128 + int(status.Signal()), true
		}
	}

	// Handle errors that directly wrap exec sentinels (e.g., ErrFZFNotFound).
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return 127, true
	case errors.Is(err, exec.ErrDot):
		return 126, true
	}
	if code, ok := launchExitCode(err); ok {
		return code, true
	}
	return errnoExitCode(err)
}

// errnoExitCode reads a bare errno a failed start returned unwrapped.
func errnoExitCode(err error) (int, bool) {
	errno, ok := directErrno(err)
	if !ok {
		return 0, false
	}
	switch errno {
	case syscall.ENOENT:
		return 127, true
	case syscall.EACCES, syscall.ENOEXEC:
		return 126, true
	}
	return 0, false
}

// launchExitCode reads the *exec.Error or exec *os.PathError a failed start
// returns.
func launchExitCode(err error) (int, bool) {
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		switch {
		case errors.Is(execErr, exec.ErrNotFound), errors.Is(execErr, fs.ErrNotExist):
			return 127, true
		case errors.Is(execErr, fs.ErrPermission), errors.Is(execErr, syscall.ENOEXEC):
			return 126, true
		}
	}

	var pathErr *os.PathError
	if errors.As(err, &pathErr) && (pathErr.Op == "fork/exec" || pathErr.Op == "exec") {
		switch {
		case errors.Is(pathErr, fs.ErrNotExist):
			return 127, true
		case errors.Is(pathErr, fs.ErrPermission), errors.Is(pathErr, syscall.ENOEXEC):
			return 126, true
		}
	}
	return 0, false
}

func isBrokenPipe(err error) bool {
	return errors.Is(err, syscall.EPIPE) || errors.Is(err, io.ErrClosedPipe)
}

func directErrno(err error) (syscall.Errno, bool) {
	for err != nil {
		switch e := err.(type) {
		case syscall.Errno:
			return e, true
		case *os.PathError, *exec.Error, *exec.ExitError:
			return 0, false
		case interface{ Unwrap() error }:
			err = e.Unwrap()
		default:
			return 0, false
		}
	}
	return 0, false
}
