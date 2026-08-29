package vaultgit

import (
	"path"
	"strings"
)

// The tracked scope is fixed in code rather than read from the vault's
// `.gitignore`. Both exist and they answer different questions: `.gitignore`
// tells the *user's* Git what to leave out of `git status`, and this policy
// tells the engine what it is allowed to add to the history on its own
// initiative. Deriving one from the other would make a hand-edited ignore rule
// silently change what BrainNotes backs up, which is the class of silent
// exclusion that already cost this vault a note once — the user's global
// core.excludesFile matched `BRAIN[A-Z]*` and quietly dropped `brainnotes.toml`
// until the vault's own ignore file neutralized it.
//
// The policy can only ever *narrow*, and only for additions. Anything already
// tracked in HEAD is in scope no matter what this says, so a path can never
// fall out of the history because the policy changed underneath it. See
// classifyPending.
const (
	bnotesPrefix        = ".bnotes/"
	bnotesJournalPrefix = ".bnotes/journal/"
)

// scopeExcludedRoots are top-level directories excluded wholesale. `backups/`
// is BrainNotes' own rolling local backup tree, which the content history
// replaces; `.codegraph/` is a regenerable code index; `.git/` is the
// repository itself; `.vault-cache/` was excluded by the pre-migration vault's
// own ignore file and is kept excluded so behavior did not change at cutover.
var scopeExcludedRoots = map[string]struct{}{
	"backups":      {},
	".codegraph":   {},
	".git":         {},
	".vault-cache": {},
}

// bnotesAllowlist is the durable generated state that belongs in the history.
// `.bnotes/` is otherwise excluded wholesale — as an allowlist rather than a
// deny-list, so state files a future version adds cannot silently start being
// committed. `.bnotes/index/` and `.bnotes/locks/` are the two that exist today
// and both are machine-local caches.
var bnotesAllowlist = map[string]struct{}{
	".bnotes/brief.json":   {},
	".bnotes/cockpit.json": {},
	".bnotes/recall.json":  {},
}

// scratchPrefixes are the sibling temp-file prefixes `internal/vault` creates
// next to a destination anywhere in the vault: `.note-<hex>.tmp`,
// `.note-mode-<hex>.tmp`, `.txn-<hex>.tmp`, `.txn-custody-<hex>.tmp`, and
// `.txn-probe-<hex>.tmp`. They are in-flight state for a write that has not
// landed, so committing one would record a half-written note as history.
var scratchPrefixes = []string{".note-", ".txn-"}

// InScope reports whether a vault-relative slash-separated path is one
// BrainNotes may add to the content history on its own.
//
// It is total and side-effect free: it stats nothing and reads no
// configuration, so the same path always gets the same answer on every machine.
// A path that escapes the vault, is absolute, or carries a `.` or `..`
// component is out of scope rather than an error — the caller's job is to skip
// it, not to fail.
func InScope(rel string) bool {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return false
	}
	// Git always reports slash-separated relative paths, but a caller may hand
	// us an OS path. Backslash is a legal character in a POSIX filename, so it
	// is not translated: a name containing one is simply judged as-is.
	if strings.HasPrefix(rel, "/") {
		return false
	}
	if rel != path.Clean(rel) {
		return false
	}
	parts := strings.Split(rel, "/")
	for _, part := range parts {
		switch part {
		case "", ".", "..", ".git", ".DS_Store":
			return false
		}
	}
	base := parts[len(parts)-1]
	if isScratchName(base) {
		return false
	}
	if _, excluded := scopeExcludedRoots[parts[0]]; excluded {
		return false
	}
	if strings.HasPrefix(rel, bnotesPrefix) {
		if strings.HasPrefix(rel, bnotesJournalPrefix) && len(rel) > len(bnotesJournalPrefix) {
			return true
		}
		_, allowed := bnotesAllowlist[rel]
		return allowed
	}
	return true
}

// isScratchName reports whether base is one of the transaction scratch or
// custody temp files. The prefixes are matched rather than the full shapes
// because the hex body varies and a future prefix variant should stay excluded
// by default: a `.txn-`-prefixed `.tmp` file is never durable content.
func isScratchName(base string) bool {
	if !strings.HasSuffix(base, ".tmp") {
		return false
	}
	for _, prefix := range scratchPrefixes {
		if strings.HasPrefix(base, prefix) {
			return true
		}
	}
	return false
}

// journalEntryID reports the journal entry a vault-relative path belongs to,
// and whether the path is that entry's manifest. It is how a pending change set
// is scanned for the records that authorize its deletions.
func journalEntryID(rel string) (id string, isManifest bool) {
	if !strings.HasPrefix(rel, bnotesJournalPrefix) {
		return "", false
	}
	rest := rel[len(bnotesJournalPrefix):]
	slash := strings.IndexByte(rest, '/')
	if slash <= 0 {
		return "", false
	}
	return rest[:slash], rest[slash+1:] == manifestFileName
}
