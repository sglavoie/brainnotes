package cli

import (
	"fmt"
	"io"

	"github.com/sglavoie/sglavoie_brainnotes/internal/buildinfo"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsyncd"
)

// bnBuildID is this binary's build identifier. It is a variable so a test can
// be a different build from the companion records it reads.
var bnBuildID = buildinfo.ID

// companionBuildWarnings reports each companion whose recorded build differs
// from this bn.
//
// `just install` replaces all three binaries together, but a LaunchAgent keeps
// running the companion it started until it is restarted, so the binaries on
// disk can agree while the processes do not. Each companion writes its build
// into its own record; this compares those with the running bn. Both reads are
// plain file reads that take no lock and change nothing, an unreadable or
// absent record says nothing, and an unknown build on either side is not
// evidence of skew.
func companionBuildWarnings(root string) []string {
	self := bnBuildID()
	var warnings []string
	if record, found, err := vaultgit.ReadGuardRecord(root); found && err == nil && buildinfo.Mismatch(record.Build, self) {
		warnings = append(warnings, fmt.Sprintf(
			"bn-vault-guard (pid %d, started %s) is build %s but this bn is build %s; restart the guard LaunchAgent so the installed build runs",
			record.PID, record.StartedAt.UTC().Format("2006-01-02T15:04:05Z"), buildinfo.Short(record.Build), buildinfo.Short(self)))
	}
	if record, err := vaultsyncd.LoadRecord(root); err == nil && buildinfo.Mismatch(record.Build, self) {
		warnings = append(warnings, fmt.Sprintf(
			"bn-vault-sync last ran as build %s but this bn is build %s; restart the sync LaunchAgent so the installed build runs",
			buildinfo.Short(record.Build), buildinfo.Short(self)))
	}
	return warnings
}

// writeCompanionBuildWarnings prints the warnings on w, which is stderr so a
// command's stdout keeps carrying only its own report.
func writeCompanionBuildWarnings(w io.Writer, root string) {
	for _, warning := range companionBuildWarnings(root) {
		_, _ = fmt.Fprintln(w, "bn: warning:", warning)
	}
}
