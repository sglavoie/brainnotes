package web

import (
	"strings"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/journal"
	"github.com/sglavoie/brainnotes/internal/vaultgit"
	"github.com/sglavoie/brainnotes/internal/vaultsync"
)

// A journal entry records the command that wrote it and nothing else about
// where the change came from, so somebody looking for a specific earlier version
// has to read `bn serve property schema upsert` and work out that it was a
// settings change rather than a note edit. historyOrigin is that reading done
// once, on the server, so the answer is the same in the panel, in the workspace,
// and in a filter.
//
// The axis is deliberately not "which process ran it". It is the recognizable
// kind of origin a person would name when asked where a change came from: made
// here in the browser, arrived from the other replica, decided in a conflict,
// recovered from this history. A settings change is `settings` whether it came
// from the browser or a terminal, because a person looking for the moment a
// template changed is looking for the change, not the keyboard.
type historyOrigin string

const (
	// historyOriginBrowser is a change made in this web interface: a source save,
	// a capture, a rename, pins, a bulk operation, a cockpit fix.
	historyOriginBrowser historyOrigin = "browser"
	// historyOriginSettings is configuration, templates, saved views, or property
	// schema — the files that describe the vault rather than fill it.
	historyOriginSettings historyOrigin = "settings"
	// historyOriginImport is a package applied from the other replica by an
	// explicit `bn sync apply`.
	historyOriginImport historyOrigin = "import"
	// historyOriginCompanion is a change the background synchronization companion
	// landed. It is separate from import because the recorded command cannot tell
	// the two apart: vaultsyncd passes its own name down into both the import and
	// the resolution-revision paths, so an entry recorded as `bn-vault-sync` is an
	// import *or* a resolution that pass carried forward, and claiming either one
	// specifically would be a guess presented as a fact.
	historyOriginCompanion historyOrigin = "companion"
	// historyOriginResolution is a synchronization conflict decision.
	historyOriginResolution historyOrigin = "resolution"
	// historyOriginRestore is an earlier recovery made from this history.
	historyOriginRestore historyOrigin = "restore"
	// historyOriginTerminal is a bn command run at a terminal.
	historyOriginTerminal historyOrigin = "terminal"
	// historyOriginUnknown is a command this table does not recognize. It is a
	// real answer rather than a default: a vault whose journal holds an entry
	// written by a version that named its commands differently should say so,
	// not be filed under whichever bucket happens to sort first.
	historyOriginUnknown historyOrigin = "unknown"
)

// historyOrigins lists every origin in the order a filter offers them, newest
// and most local first. A client reads the set from the server rather than
// hard-coding it, so a version that learns a new origin does not need the
// browser rebuilt to let somebody filter by it.
var historyOrigins = []historyOrigin{
	historyOriginBrowser,
	historyOriginSettings,
	historyOriginImport,
	historyOriginCompanion,
	historyOriginResolution,
	historyOriginRestore,
	historyOriginTerminal,
	historyOriginUnknown,
}

var historyOriginLabels = map[historyOrigin]string{
	historyOriginBrowser:    "Saved in the browser",
	historyOriginSettings:   "Settings and templates",
	historyOriginImport:     "Imported from your other replica",
	historyOriginCompanion:  "Landed by the sync companion",
	historyOriginResolution: "Conflict resolution",
	historyOriginRestore:    "Restored from history",
	historyOriginTerminal:   "Run at the terminal",
	historyOriginUnknown:    "Unrecognized command",
}

func (o historyOrigin) label() string {
	if label, ok := historyOriginLabels[o]; ok {
		return label
	}
	return historyOriginLabels[historyOriginUnknown]
}

// historyOriginRule matches a recorded command by prefix.
type historyOriginRule struct {
	prefix string
	origin historyOrigin
}

// historyOriginRules is ordered most specific first and read in order, because
// several of these prefixes contain each other: `bn serve sync resolve` is also
// `bn serve `, and `bn sync resolve` is not `bn sync apply` but shares a
// family with it. The list is the whole classification — there is no second
// pass — so a command that matches nothing here lands in historyOriginUnknown
// rather than being approximated.
var historyOriginRules = []historyOriginRule{
	{"bn web undo", historyOriginRestore},
	{"bn undo", historyOriginRestore},
	{"bn serve sync resolve", historyOriginResolution},
	{"bn sync resolve", historyOriginResolution},
	{"bn sync apply", historyOriginImport},
	{"bn-vault-sync", historyOriginCompanion},
	{"bn serve setting ", historyOriginSettings},
	{"bn serve template ", historyOriginSettings},
	{"bn serve view ", historyOriginSettings},
	{"bn serve property schema ", historyOriginSettings},
	{"bn config ", historyOriginSettings},
	{"bn serve ", historyOriginBrowser},
	{"bn web ", historyOriginBrowser},
	{"bn cockpit ", historyOriginBrowser},
	{"bn ", historyOriginTerminal},
}

// historyOriginForCommand classifies one recorded command.
func historyOriginForCommand(command string) historyOrigin {
	command = strings.TrimSpace(command)
	if command == "" {
		return historyOriginUnknown
	}
	for _, rule := range historyOriginRules {
		if strings.HasPrefix(command, rule.prefix) {
			return rule.origin
		}
	}
	return historyOriginUnknown
}

// parseHistoryOrigin reads an origin named by a client, and reports whether it
// is one this server knows. An unknown name is refused rather than ignored: a
// filter nobody's server understands must not quietly answer as though no filter
// had been asked for, because the reply would look like "these are all the
// entries of that kind" when it is "these are all the entries".
func parseHistoryOrigin(value string) (historyOrigin, bool) {
	candidate := historyOrigin(strings.ToLower(strings.TrimSpace(value)))
	for _, origin := range historyOrigins {
		if origin == candidate {
			return origin, true
		}
	}
	return "", false
}

// historyPathClass says what kind of file one recorded path is. It answers the
// part of the session's question that a command name cannot: an attachment is
// not an operation, it is content, and it is written inside the same
// `bn web source save` entry as the note that references it.
type historyPathClass string

const (
	historyPathNote          historyPathClass = "note"
	historyPathAttachment    historyPathClass = "attachment"
	historyPathTemplate      historyPathClass = "template"
	historyPathConfiguration historyPathClass = "configuration"
	historyPathState         historyPathClass = "state"
	historyPathOther         historyPathClass = "other"
)

// historyPathClasses lists the classes in the order they are offered and
// counted, so two entries with the same mix of files describe it the same way.
var historyPathClasses = []historyPathClass{
	historyPathNote,
	historyPathAttachment,
	historyPathTemplate,
	historyPathConfiguration,
	historyPathState,
	historyPathOther,
}

var historyPathClassLabels = map[historyPathClass]string{
	historyPathNote:          "Notes",
	historyPathAttachment:    "Attachments",
	historyPathTemplate:      "Templates",
	historyPathConfiguration: "Configuration",
	historyPathState:         "Vault state",
	historyPathOther:         "Other files",
}

func (c historyPathClass) label() string {
	if label, ok := historyPathClassLabels[c]; ok {
		return label
	}
	return historyPathClassLabels[historyPathOther]
}

func parseHistoryPathClass(value string) (historyPathClass, bool) {
	candidate := historyPathClass(strings.ToLower(strings.TrimSpace(value)))
	for _, class := range historyPathClasses {
		if class == candidate {
			return class, true
		}
	}
	return "", false
}

// historyPathClassifier answers what kind of file a recorded path is under the
// configuration in force.
//
// It is built per request from the reloaded configuration rather than held on
// the Server, for the reason attachmentStore gives: both directories it keys on
// are settings, and a vault that moved its attachments directory must have the
// next answer use the new value. The older entries in the same list were written
// under the old value and will be classified under the new one — which is
// honest, because the question "is this an attachment" is asked of the vault as
// it is configured now, and the path itself is always shown beside the class.
type historyPathClassifier struct {
	attachmentsDir string
	templatesDir   string
}

func newHistoryPathClassifier(cfg *config.Config) historyPathClassifier {
	if cfg == nil {
		return historyPathClassifier{}
	}
	return historyPathClassifier{
		attachmentsDir: strings.Trim(strings.TrimSpace(cfg.Attachments.Dir), "/"),
		templatesDir:   strings.Trim(strings.TrimSpace(cfg.Templates.Dir), "/"),
	}
}

// vaultStateDirPrefix is the directory the vault's own machinery owns. Nothing
// under it is a note, whatever its extension.
const vaultStateDirPrefix = ".bnotes/"

func (c historyPathClassifier) classify(rel string) historyPathClass {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return historyPathOther
	}
	switch rel {
	case config.FileName, config.LocalFileName:
		return historyPathConfiguration
	}
	if strings.HasPrefix(rel, vaultStateDirPrefix) {
		return historyPathState
	}
	if withinHistoryDir(rel, c.attachmentsDir) {
		return historyPathAttachment
	}
	if withinHistoryDir(rel, c.templatesDir) {
		return historyPathTemplate
	}
	if strings.EqualFold(pathExtension(rel), ".md") {
		return historyPathNote
	}
	return historyPathOther
}

// withinHistoryDir reports whether a vault-relative path sits under a
// vault-relative directory. An empty directory matches nothing rather than
// everything: an unset setting must not turn the whole vault into attachments.
func withinHistoryDir(rel, dir string) bool {
	if dir == "" {
		return false
	}
	return strings.HasPrefix(rel, dir+"/")
}

func pathExtension(rel string) string {
	idx := strings.LastIndex(rel, ".")
	if idx < 0 {
		return ""
	}
	if strings.Contains(rel[idx:], "/") {
		return ""
	}
	return rel[idx:]
}

// historyFileFacts is what one recorded path is, beyond its name.
//
// Recorded and Travels are two different questions and are reported separately
// because the session's completion turns on the second one. Recorded says the
// path enters the content history, so restoring it becomes a commit. Travels
// says the path is portable, so that commit reaches the other replica.
//
// They are genuinely independent, and the journal is the case that proves it:
// `.bnotes/journal/` is kept in the content history — so an undo record survives
// being restored from the remote — while nothing under `.bnotes/` is portable,
// so that commit stays on this machine. The machine-local configuration overlay
// is in neither. A surface that promised "synchronized as a new change" for
// every restored path would be promising something the scope rules refuse, so
// both answers are reported per path and neither is inferred from the other.
type historyFileFacts struct {
	Class    historyPathClass
	Recorded bool
	Travels  bool
}

func (c historyPathClassifier) facts(rel string) historyFileFacts {
	return historyFileFacts{
		Class:    c.classify(rel),
		Recorded: vaultgit.InScope(rel),
		Travels:  vaultsync.Portable(rel),
	}
}

// historyEntryFacts is the summary a list row needs: where the change came from
// and what kinds of file it touched, without the reader opening it.
type historyEntryFacts struct {
	Origin  historyOrigin
	Classes map[historyPathClass]int
}

func (c historyPathClassifier) entryFacts(entry journal.Entry) historyEntryFacts {
	classes := make(map[historyPathClass]int, len(historyPathClasses))
	for _, file := range entry.Files {
		classes[c.classify(file.Path)]++
	}
	return historyEntryFacts{
		Origin:  historyOriginForCommand(entry.Command),
		Classes: classes,
	}
}
