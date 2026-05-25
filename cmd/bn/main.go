// Command bn is the entrypoint for Brain Notes, a CLI for plain-markdown vaults.
package main

import (
	"errors"
	"fmt"
	"io"
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
	_, _ = fmt.Fprintln(stderr, "bn:", err)
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
	return 1
}

func isBrokenPipe(err error) bool {
	return errors.Is(err, syscall.EPIPE) || errors.Is(err, io.ErrClosedPipe)
}
