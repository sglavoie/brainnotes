// Package cockpit provides a read-only, vault-aware queue of hygiene tasks.
//
// It loads the vault once, builds a link graph, and groups the current
// diagnostics (frontmatter parse errors, schema violations, broken targets,
// and duplicate titles) into deterministic tasks. The model here is the shared
// core that the CLI doctor and web cockpit build on; it deliberately does not
// know about HTTP routes or terminal rendering. Later sessions layer fix
// planning and preview onto these types without changing the loader.
package cockpit

// RuleKind identifies the hygiene rule that produced a task. Values are stable
// strings so signatures and stored workflow state survive across runs.
type RuleKind string

const (
	// RuleFrontmatterError flags a note whose YAML frontmatter fails to parse.
	RuleFrontmatterError RuleKind = "frontmatter_error"
	// RuleSchemaViolation flags notes that break a configured property schema.
	RuleSchemaViolation RuleKind = "schema_violation"
	// RuleBrokenTarget flags a wikilink target that resolves to no note.
	RuleBrokenTarget RuleKind = "broken_target"
	// RuleDuplicateTitle flags a title shared by two or more notes.
	RuleDuplicateTitle RuleKind = "duplicate_title"
	// RuleOrphanNote flags a note that has no inbound links from other notes.
	RuleOrphanNote RuleKind = "orphan_note"
	// RuleDeadEndNote flags a note that has no outgoing links to other notes.
	RuleDeadEndNote RuleKind = "dead_end_note"
	// RuleLongTailValue flags a rarely-used frontmatter property value that may
	// want consolidating into an existing value.
	RuleLongTailValue RuleKind = "long_tail_value"
)

// rulePriority orders rule kinds in the queue. Lower sorts first. Unknown kinds
// fall to the end so new rules stay visible until they are given a weight.
func rulePriority(kind RuleKind) int {
	switch kind {
	case RuleFrontmatterError:
		return 10
	case RuleSchemaViolation:
		return 20
	case RuleBrokenTarget:
		return 30
	case RuleDuplicateTitle:
		return 40
	case RuleOrphanNote:
		return 50
	case RuleDeadEndNote:
		return 60
	case RuleLongTailValue:
		return 70
	default:
		return 100
	}
}

// ruleTriageOnly reports whether a rule kind only ever produces triage tasks —
// findings a human must resolve by judgement rather than a mechanical fix that
// the cockpit could apply. Schema violations and broken targets are the two
// kinds that can carry an automatic fix (planned in a later session); every
// other kind (parse errors, duplicate titles, orphans, dead ends, and long-tail
// values) is triage-only.
func ruleTriageOnly(kind RuleKind) bool {
	switch kind {
	case RuleSchemaViolation, RuleBrokenTarget:
		return false
	default:
		return true
	}
}

// ActionKind identifies a proposed remediation for a task. The triage actions
// (open/snooze/mark-done) apply to every task; the fix actions are only offered
// on the rule kinds whose findings a planner can mechanically resolve.
type ActionKind string

const (
	// ActionOpen opens the affected note(s) so a human can inspect or fix the
	// finding by hand. Every task offers it.
	ActionOpen ActionKind = "open"
	// ActionSnooze hides the task until later (the "filter" triage action).
	ActionSnooze ActionKind = "snooze"
	// ActionMarkDone marks the task resolved so it drops out of the queue.
	ActionMarkDone ActionKind = "mark_done"
	// ActionSchemaRepair runs the schema repair planner over the affected notes.
	ActionSchemaRepair ActionKind = "schema_repair"
	// ActionRelink rewrites the broken wikilink to a deterministic target.
	ActionRelink ActionKind = "relink"
	// ActionCreateStub creates a blank stub note for a broken wikilink target.
	ActionCreateStub ActionKind = "create_stub"
)

// Action is a single proposed remediation for a task. Fix actions are backed by
// the preview/apply planners in fix.go; triage actions are handled by callers
// (the editor for open, the StateStore for snooze/mark-done).
type Action struct {
	Kind  ActionKind `json:"kind"`
	Label string     `json:"label"`
}

// actionsForRule returns the ordered action set a task of the given rule kind
// exposes. Every task can be opened, snoozed, or marked done; fixable kinds add
// their mechanical fix action(s) between the open and triage affordances.
func actionsForRule(kind RuleKind) []Action {
	actions := []Action{{Kind: ActionOpen, Label: "Open note"}}
	switch kind {
	case RuleSchemaViolation:
		actions = append(actions, Action{Kind: ActionSchemaRepair, Label: "Repair schema"})
	case RuleBrokenTarget:
		actions = append(actions,
			Action{Kind: ActionRelink, Label: "Relink"},
			Action{Kind: ActionCreateStub, Label: "Create stub note"},
		)
	}
	actions = append(actions,
		Action{Kind: ActionSnooze, Label: "Snooze"},
		Action{Kind: ActionMarkDone, Label: "Mark done"},
	)
	return actions
}

// AffectedNote is a note implicated in a task, with enough location context to
// point a human (or a later fix) at the exact place to change.
type AffectedNote struct {
	Title    string `json:"title"`
	RelPath  string `json:"rel_path"`
	Folder   string `json:"folder,omitempty"`
	Location string `json:"location,omitempty"`
	// FileHash is the SHA-256 of the note's on-disk bytes when it was loaded.
	// It anchors task signatures so a task is treated as unchanged only while
	// the underlying file is unchanged. Not serialized to callers.
	FileHash string `json:"-"`
}

// Warning is a non-fatal problem encountered while building the queue that is
// not itself a task (for example, a load hiccup that did not stop the walk).
type Warning struct {
	Message string `json:"message"`
	Path    string `json:"path,omitempty"`
}

// Task is one grouped hygiene finding. Tasks are read-only: they describe what
// is wrong and, eventually, what could fix it.
type Task struct {
	ID        string         `json:"id"`
	Signature string         `json:"signature"`
	Kind      RuleKind       `json:"kind"`
	Priority  int            `json:"priority"`
	Title     string         `json:"title"`
	Summary   string         `json:"summary"`
	Detail    string         `json:"detail,omitempty"`
	Count     int            `json:"count"`
	Affected  []AffectedNote `json:"affected"`
	Actions   []Action       `json:"actions,omitempty"`
	// TriageOnly marks a task that can never be resolved by an automatic fix —
	// it always requires human judgement. Callers use it to hide "apply" affordances
	// and to keep the automatic-fix queue distinct from the triage queue.
	TriageOnly bool `json:"triage_only"`
}

// Counts summarizes the queue for headers and badges. DoneHidden and
// SnoozedHidden report how many generated tasks a StateStore suppressed; they
// are zero on the raw queue that BuildQueue returns before filtering.
type Counts struct {
	Active         int `json:"active"`
	DoneHidden     int `json:"done_hidden"`
	SnoozedHidden  int `json:"snoozed_hidden"`
	TotalGenerated int `json:"total_generated"`
}

// Queue is the full read-only cockpit view for a vault snapshot.
type Queue struct {
	Tasks    []Task    `json:"tasks"`
	Counts   Counts    `json:"counts"`
	Warnings []Warning `json:"warnings,omitempty"`
}
