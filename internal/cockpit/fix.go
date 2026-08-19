package cockpit

import (
	"fmt"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/schema"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// defaultFixCommand is the label stored in undo-journal entries for cockpit
// fixes when the caller does not override it.
const defaultFixCommand = "bn cockpit fix"

// FixSkip records a note or target a fix planner could not handle, with a short
// human-readable reason. Skips are informational: they never fail the whole
// plan, so a partially-fixable task still surfaces the changes it can make.
type FixSkip struct {
	RelPath string `json:"rel_path,omitempty"`
	Target  string `json:"target,omitempty"`
	Reason  string `json:"reason"`
}

// FixPlan is the read-only preview of applying a fix action to a task. Previews
// only lists notes with at least one change; Skipped explains everything the
// planner left untouched.
type FixPlan struct {
	Action   ActionKind            `json:"action"`
	TaskID   string                `json:"task_id"`
	Previews []noteops.NotePreview `json:"previews,omitempty"`
	Skipped  []FixSkip             `json:"skipped,omitempty"`
}

// Changed reports whether the plan carries any concrete change.
func (p FixPlan) Changed() bool {
	for _, preview := range p.Previews {
		if preview.Changed() {
			return true
		}
	}
	return false
}

// FixOutcome is the result of applying a fix action. Changed lists the
// vault-relative paths that were rewritten; Created lists new notes; Skipped
// mirrors the plan's skips. Journal is the single undo entry covering every
// mutation the fix made (nil when nothing was written).
type FixOutcome struct {
	Action  ActionKind     `json:"action"`
	TaskID  string         `json:"task_id"`
	Changed []string       `json:"changed,omitempty"`
	Created []string       `json:"created,omitempty"`
	Skipped []FixSkip      `json:"skipped,omitempty"`
	Journal *journal.Entry `json:"-"`
}

// Fixer plans and applies safe fixes for cockpit tasks against a vault. By
// default it reloads the vault for each Plan/Apply call so previews and applies
// always see current on-disk state; at the documented working size (~300 notes)
// the reload is cheap and keeps the planners stateless. A Fixer built with
// NewFixerFromSnapshot instead reuses a preloaded view, so a caller that already
// performed a bounded/resilient load (e.g. bn serve) never triggers a second,
// unbounded walk that could stall on a quarantined note.
type Fixer struct {
	vault    *vault.Vault
	cfg      *config.Config
	command  string
	snapshot *Snapshot
}

// NewFixer returns a Fixer bound to a vault and its config. command, when set,
// is recorded in undo-journal entries for applied fixes.
func NewFixer(v *vault.Vault, cfg *config.Config, command string) *Fixer {
	command = strings.TrimSpace(command)
	if command == "" {
		command = defaultFixCommand
	}
	if cfg == nil {
		cfg = &config.Config{}
	}
	return &Fixer{vault: v, cfg: cfg, command: command}
}

// NewFixerFromSnapshot returns a Fixer that plans and applies against an
// already-loaded vault view instead of walking the vault on each Plan/Apply.
// Callers that hold a resilient, bounded snapshot (e.g. bn serve, which excludes
// notes whose read timed out) use this so a stalled note the snapshot already
// dropped is never re-read during a fix. The snapshot must describe current
// on-disk state for the notes a fix touches, since the planners diff against it;
// bn serve refreshes the snapshot immediately before fixing to guarantee this.
// The fix's actual writes still go through noteops against the live filesystem,
// so destination-conflict detection is unaffected by the preloaded view.
func NewFixerFromSnapshot(v *vault.Vault, cfg *config.Config, command string, snapshot Snapshot) *Fixer {
	f := NewFixer(v, cfg, command)
	snap := snapshot
	f.snapshot = &snap
	return f
}

// Plan previews the effect of applying action to task without writing anything.
// Only the mutating fix actions (schema repair, relink, create stub) have
// planners; triage actions (open/snooze/mark-done) return an error because they
// are handled by the editor and the StateStore, not a preview.
func (f *Fixer) Plan(task Task, action ActionKind) (FixPlan, error) {
	data, byRel, err := f.loadContext(task, action)
	if err != nil {
		return FixPlan{}, err
	}
	plan := FixPlan{Action: action, TaskID: task.ID}
	switch action {
	case ActionSchemaRepair:
		return f.planSchemaRepair(plan, task, byRel)
	case ActionRelink:
		return f.planRelink(plan, task, data, byRel)
	case ActionCreateStub:
		return f.planCreateStub(plan, task, data, byRel)
	default:
		return FixPlan{}, unfixableAction(action)
	}
}

// Apply performs the fix described by action against task, journaling every
// mutation through noteops.NewJournal so the change is undoable. Like Plan, it
// only accepts the mutating fix actions.
func (f *Fixer) Apply(task Task, action ActionKind) (FixOutcome, error) {
	data, byRel, err := f.loadContext(task, action)
	if err != nil {
		return FixOutcome{}, err
	}
	out := FixOutcome{Action: action, TaskID: task.ID}
	switch action {
	case ActionSchemaRepair:
		return f.applySchemaRepair(out, task, byRel)
	case ActionRelink:
		return f.applyRelink(out, task, data, byRel)
	case ActionCreateStub:
		return f.applyCreateStub(out, task, data, byRel)
	default:
		return FixOutcome{}, unfixableAction(action)
	}
}

// loadContext validates the action against the task and loads the vault once,
// returning the snapshot plus a lookup from relative path to note handle.
func (f *Fixer) loadContext(task Task, action ActionKind) (vaultData, map[string]*vault.Note, error) {
	if f == nil || f.vault == nil {
		return vaultData{}, nil, fmt.Errorf("cockpit: nil fixer or vault")
	}
	if !taskOffersAction(task, action) {
		return vaultData{}, nil, fmt.Errorf("cockpit: task %s does not offer action %q", task.ID, action)
	}
	data, err := f.loadData()
	if err != nil {
		return vaultData{}, nil, err
	}
	byRel := make(map[string]*vault.Note, len(data.loaded))
	for _, item := range data.loaded {
		byRel[item.rel] = item.note
	}
	return data, byRel, nil
}

// loadData returns the vault view the planners diff against: the preloaded
// snapshot when the Fixer was built with one (avoiding a second, potentially
// unbounded walk), otherwise a fresh full load.
func (f *Fixer) loadData() (vaultData, error) {
	if f.snapshot != nil {
		return vaultDataFromSnapshot(*f.snapshot), nil
	}
	return loadVaultData(f.vault)
}

// taskOffersAction reports whether task advertises action in its Actions set.
func taskOffersAction(task Task, action ActionKind) bool {
	for _, a := range task.Actions {
		if a.Kind == action {
			return true
		}
	}
	return false
}

func unfixableAction(action ActionKind) error {
	return fmt.Errorf("cockpit: action %q has no fix planner", action)
}

// --- schema repair ---------------------------------------------------------

// schemaProperty extracts the schema property a schema-violation task targets
// from its affected notes' locations ("frontmatter.<property>").
func schemaProperty(task Task) string {
	for _, note := range task.Affected {
		if prop := strings.TrimPrefix(note.Location, "frontmatter."); prop != note.Location {
			if prop != "" {
				return prop
			}
		}
	}
	return ""
}

func (f *Fixer) planSchemaRepair(plan FixPlan, task Task, byRel map[string]*vault.Note) (FixPlan, error) {
	property := schemaProperty(task)
	for _, affected := range task.Affected {
		note, ok := byRel[affected.RelPath]
		if !ok {
			plan.Skipped = append(plan.Skipped, FixSkip{RelPath: affected.RelPath, Reason: "note no longer present"})
			continue
		}
		actions, skips, err := f.planNoteRepairs(note, property)
		if err != nil {
			return FixPlan{}, err
		}
		plan.Skipped = append(plan.Skipped, skips...)
		if len(actions) == 0 {
			continue
		}
		preview := noteops.NotePreview{RelPath: affected.RelPath}
		for _, act := range actions {
			preview.Changes = append(preview.Changes,
				noteops.FrontmatterChange(act.Property, true, act.OldValue, true, act.NewValue))
		}
		plan.Previews = append(plan.Previews, preview)
	}
	return plan, nil
}

func (f *Fixer) applySchemaRepair(out FixOutcome, task Task, byRel map[string]*vault.Note) (FixOutcome, error) {
	property := schemaProperty(task)
	j := noteops.NewJournal(f.vault.Root, f.command)
	for _, affected := range task.Affected {
		note, ok := byRel[affected.RelPath]
		if !ok {
			out.Skipped = append(out.Skipped, FixSkip{RelPath: affected.RelPath, Reason: "note no longer present"})
			continue
		}
		actions, skips, err := f.planNoteRepairs(note, property)
		if err != nil {
			return FixOutcome{}, err
		}
		out.Skipped = append(out.Skipped, skips...)
		if len(actions) == 0 {
			continue
		}
		if err := j.Capture(note.Path); err != nil {
			return FixOutcome{}, err
		}
		front, err := note.Front()
		if err != nil {
			return FixOutcome{}, err
		}
		for _, act := range actions {
			schema.ApplyRepairAction(front, act, f.cfg.Frontmatter.Properties)
		}
		if err := note.Save(&f.cfg.Frontmatter); err != nil {
			return FixOutcome{}, err
		}
		out.Changed = append(out.Changed, affected.RelPath)
	}
	entry, err := j.Finalize()
	if err != nil {
		return FixOutcome{}, err
	}
	out.Journal = entry
	return out, nil
}

// planNoteRepairs runs the schema repair planner over a single note, scoping to
// property when set. It returns the safe actions plus a skip per unfixable
// issue in the same scope.
func (f *Fixer) planNoteRepairs(note *vault.Note, property string) ([]schema.RepairAction, []FixSkip, error) {
	front, err := note.Front()
	if err != nil {
		return nil, nil, err
	}
	props := f.cfg.Frontmatter.Properties
	if property != "" {
		def, ok := props[property]
		if !ok {
			return nil, nil, nil
		}
		props = map[string]config.PropertyDefinition{property: def}
	}
	result := schema.PlanRepairs(front, props)

	rel := relPath(note.Path, f.vault.Root)
	var skips []FixSkip
	for _, s := range result.Skips {
		if property != "" && s.Property != property {
			continue
		}
		skips = append(skips, FixSkip{RelPath: rel, Reason: s.Property + ": " + s.Detail})
	}
	var actions []schema.RepairAction
	for _, a := range result.Actions {
		if property != "" && a.Property != property {
			continue
		}
		actions = append(actions, a)
	}
	return actions, skips, nil
}

// --- relink ----------------------------------------------------------------

func (f *Fixer) planRelink(plan FixPlan, task Task, data vaultData, byRel map[string]*vault.Note) (FixPlan, error) {
	idx := newRelinkIndex(data.loaded)
	for _, affected := range task.Affected {
		note, ok := byRel[affected.RelPath]
		if !ok {
			plan.Skipped = append(plan.Skipped, FixSkip{RelPath: affected.RelPath, Reason: "note no longer present"})
			continue
		}
		rewrite, err := planNoteRelink(note, affected.RelPath, data.graph, idx, task.Detail)
		if err != nil {
			return FixPlan{}, err
		}
		plan.Skipped = append(plan.Skipped, rewrite.skips...)
		if len(rewrite.changes) == 0 {
			continue
		}
		preview := noteops.NotePreview{RelPath: affected.RelPath, Changes: rewrite.changes}
		plan.Previews = append(plan.Previews, preview)
	}
	return plan, nil
}

func (f *Fixer) applyRelink(out FixOutcome, task Task, data vaultData, byRel map[string]*vault.Note) (FixOutcome, error) {
	idx := newRelinkIndex(data.loaded)
	j := noteops.NewJournal(f.vault.Root, f.command)
	for _, affected := range task.Affected {
		note, ok := byRel[affected.RelPath]
		if !ok {
			out.Skipped = append(out.Skipped, FixSkip{RelPath: affected.RelPath, Reason: "note no longer present"})
			continue
		}
		rewrite, err := planNoteRelink(note, affected.RelPath, data.graph, idx, task.Detail)
		if err != nil {
			return FixOutcome{}, err
		}
		out.Skipped = append(out.Skipped, rewrite.skips...)
		if len(rewrite.changes) == 0 {
			continue
		}
		if err := j.Capture(note.Path); err != nil {
			return FixOutcome{}, err
		}
		if rewrite.newBody != nil {
			if err := note.SetBody(*rewrite.newBody); err != nil {
				return FixOutcome{}, err
			}
		}
		front, err := note.Front()
		if err != nil {
			return FixOutcome{}, err
		}
		for key, value := range rewrite.newFront {
			front.Set(key, value)
		}
		if err := note.Save(&f.cfg.Frontmatter); err != nil {
			return FixOutcome{}, err
		}
		out.Changed = append(out.Changed, affected.RelPath)
	}
	entry, err := j.Finalize()
	if err != nil {
		return FixOutcome{}, err
	}
	out.Journal = entry
	return out, nil
}

// relinkRewrite is the in-memory result of relinking a single note against the
// task's target: the canonical preview changes plus the new body/frontmatter
// values to persist on apply.
