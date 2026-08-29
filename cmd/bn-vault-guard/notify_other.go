//go:build !darwin

package main

// notifyDesktop is a no-op off macOS.
//
// The guard is built and tested on every platform the rest of the module
// supports, but the LaunchAgent that runs it is macOS-only and this plan's
// notification requirement is a Darwin one. Elsewhere the structured log is the
// whole reporting surface, which is enough: there is no supported deployment
// off macOS for a person to be notified in.
func notifyDesktop(title, body string) {
	_, _ = title, body
}
