//go:build !unix

package agent

import "os/exec"

// configureCommandCancellation keeps CommandContext's direct-process
// cancellation on platforms without Unix process groups. WaitDelay still
// bounds waits on inherited I/O pipes.
func configureCommandCancellation(_ *exec.Cmd) {}
