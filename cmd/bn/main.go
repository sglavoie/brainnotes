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
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if code := exitErr.ExitCode(); code >= 0 {
			return code
		}
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return 128 + int(status.Signal())
		}
	}

	// Handle errors that directly wrap exec sentinels (e.g., ErrFZFNotFound).
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return 127
	case errors.Is(err, exec.ErrDot):
		return 126
	}

	var execErr *exec.Error
	if errors.As(err, &execErr) {
		switch {
		case errors.Is(execErr, exec.ErrNotFound), errors.Is(execErr, fs.ErrNotExist):
			return 127
		case errors.Is(execErr, fs.ErrPermission), errors.Is(execErr, syscall.ENOEXEC):
			return 126
		}
	}

	var pathErr *os.PathError
	if errors.As(err, &pathErr) && (pathErr.Op == "fork/exec" || pathErr.Op == "exec") {
		switch {
		case errors.Is(pathErr, fs.ErrNotExist):
			return 127
		case errors.Is(pathErr, fs.ErrPermission), errors.Is(pathErr, syscall.ENOEXEC):
			return 126
		}
	}
	if errno, ok := directErrno(err); ok {
		switch errno {
		case syscall.ENOENT:
			return 127
		case syscall.EACCES, syscall.ENOEXEC:
			return 126
		}
	}

	return 1
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
