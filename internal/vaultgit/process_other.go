//go:build !unix

package vaultgit

import "os/exec"

// configureCommandCancellation keeps CommandContext's direct-process
// cancellation on platforms without Unix process groups. cmd.WaitDelay still
// bounds how long Wait blocks on a pipe a surviving grandchild holds open.
func configureCommandCancellation(_ *exec.Cmd) {}
