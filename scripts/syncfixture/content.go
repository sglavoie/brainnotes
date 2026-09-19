package main

import "time"

// The contested paths, and the one that waits behind them.
//
// Every one is under `Sync/` and none of them joins prepare.mjs's SEED_NOTES, for
// two separate reasons. The suite runs one worker over one vault pair, so a
// conflict on `Alpha.md` would be contesting the note editor.spec.js renames away
// and recreates; and `manifest.seedNotes` is what smoke.spec.js iterates as "every
// note this vault is supposed to serve", which these are not — four of them are a
// conflict's local side and one of them the spec deletes the remote version of.
const (
	textPath       = "Sync/Text.md"
	attachmentPath = "Sync/Attachments/diagram.png"
	renamePath     = "Sync/Rename.md"
	renameDest     = "Sync/Moved.md"
	renameArchive  = "Sync/Archive/Rename.md"
	doomedPath     = "Sync/Doomed.md"
	dependentPath  = "Sync/Dependent.md"
	configPath     = "brainnotes.toml"
)

// The three versions of every contested note: the baseline both replicas last
// agreed on, this vault's, and the peer's.
//
// The baselines are never written to disk. They exist only as the hash a package
// states as its expectation, which is what makes each of these a conflict — and it
// is also the honest shape of the common case, since a replica that has moved on
// no longer holds the version the package expected. The consequence is visible on
// the page and is deliberately not fought: internal/vaultimport's baseline is
// best-effort, so the review surface renders "This version could not be read."
// under the third heading for a baseline only a digest names.
const (
	textBaseline = "# Text\n\nThe sentence both replicas started from.\n"
	textLocal    = "# Text\n\nThe sentence both replicas started from.\n\nTyped on this device.\n"
	textRemote   = "# Text\n\nThe sentence both replicas started from.\n\nTyped on the other device.\n"

	// renameBaseline travels *inside* the rename package, as the bytes its
	// destination is written with, so this is the one baseline the review page can
	// show.
	renameBaseline = "# Rename\n\nA note each replica decided to file somewhere else.\n"

	doomedBaseline = "# Doomed\n\nA note the other device removed.\n"
	doomedLocal    = "# Doomed\n\nA note the other device removed.\n\nStill being written here.\n"

	dependentBody = "# Dependent\n\nWritten after the note it follows.\n"

	// localSetting and remoteSetting are property *declarations*, which is what
	// makes the configuration case a ConflictConfiguration rather than an ordinary
	// concurrent edit: each side governs notes neither package carries, so taking
	// either wholesale re-governs the whole vault. Both names are free — the
	// fixture config declares `status` and `tags` and nothing else.
	localSetting  = "\n[frontmatter.properties.area]\ntype = \"string\"\n"
	remoteSetting = "\n[frontmatter.properties.org]\ntype = \"wikilink\"\n"
)

// seededAt is the modification time this program gives the contested content it
// writes into the served vault — the five files diverge writes, which are the only
// ones a person browsing the vault ever sees — and it is deliberately older than the
// notes prepare.mjs seeds. What the engine then writes under `.bnotes/sync/` is
// dated by the engine and is not a note, so nothing here touches it.
//
// The reason is a page rather than a protocol: /library lists the twelve
// most-recently-modified notes, and smoke.spec.js asserts every note in
// `manifest.seedNotes` is on it. Notes written *now* would be the twelve newest
// and would push the suite's own subjects off the end of a list they are asserted
// to be in. Filing the contested notes behind them is also the truer story — this
// is the version both replicas last agreed on, edited a while ago — and it makes
// the fixture's timestamps deterministic, the way the enrollment and package
// clocks below already are.
var seededAt = time.Date(2024, time.February, 1, 9, 0, 0, 0, time.UTC)

// attachment returns bytes that open with a PNG signature and are therefore not
// valid UTF-8.
//
// That is the whole of the attachment case. internal/web's syncMergeable answers
// false for a version it cannot show as text, so the review page must offer
// keep-current and accept-incoming, must refuse a manual merge in so many words,
// and must not pretend either version can be read — and a spec that contested a
// second Markdown file would never exercise any of it.
func attachment(tag string) []byte {
	return append([]byte("\x89PNG\r\n\x1a\n"), tag...)
}

// labels name the packages in the order they are published, which is also their
// sequence in the producing device's stream. "dependent" is last because it names
// the text operation as a causal predecessor, and a predecessor is only held
// against a package that is still in the channel.
var labels = []string{"text", "attachment", "rename", "deletion", "settings", "dependent"}

// marks are the substrings the spec recognises each version of a conflict by on
// the rendered page, under the three headings the page shows: this vault's, the
// other replica's, and the baseline they last agreed on.
//
// Substrings rather than whole versions: the page shows the bytes inside markup,
// and an assertion on a whole file's text would be an assertion about whitespace
// collapsing. Empty means there is nothing to recognise there. Which versions
// those are is not symmetric, and is the engine's answer rather than a choice:
// the deletion's remote side is a tombstone; a divergent rename has *neither*
// branch at the contested path — the source is gone here and gone there, so both
// report as absent, and the only readable version is the baseline, which travels
// inside the package as the bytes its destination is written with; and the other
// baselines exist only as digests, so the page says it could not read them.
var marks = map[string]struct{ local, remote, baseline string }{
	"text":     {local: "Typed on this device.", remote: "Typed on the other device."},
	"deletion": {local: "Still being written here."},
	"settings": {local: "[frontmatter.properties.area]", remote: "[frontmatter.properties.org]"},
	"rename":   {baseline: "A note each replica decided to file somewhere else."},
}
