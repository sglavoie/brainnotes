//go:build unix

package agent

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// configureCommandCancellation starts the launcher and every descendant it
// spawns in a dedicated process group. Killing the group is necessary for
// wrappers such as the Codex Node entrypoint: killing only the wrapper leaves
// its native child alive with inherited stdout/stderr pipes, which prevents
// exec.Cmd.Wait from returning.
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
