package web

import (
	"fmt"
	"os/exec"
	"runtime"
)

func openBrowserDefault(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("web: unsupported OS %q for browser launch", runtime.GOOS)
	}
	return cmd.Start()
}
