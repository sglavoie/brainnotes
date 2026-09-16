package config

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// localFileHeader is the comment a migration writes when it creates the overlay
// itself. A file that appears in a vault with no explanation of why is one the
// next person deletes.
var localFileHeader = []string{
	"# Machine-local BrainNotes configuration.",
	"#",
	"# It describes this machine — the editor to launch, the address to bind, the",
	"# agent command to run — and never travels with the vault. " + FileName + " holds",
	"# everything that describes the vault itself.",
	"",
}

// MigrationMove is one key relocated from the file that held it to the file that
// owns it.
type MigrationMove struct {
	Key  string
	From Layer
	To   Layer
	// Shadowed reports that both files assigned this key, so only one of the two
	// assignments was ever in force. The overlay is the file that wins, and the
	// migration keeps that value and drops the other; see resolveShadowedMove.
	Shadowed bool
}

// MigrationPlan is a previewable, not-yet-written routing of every misplaced
// assignment to its owning configuration file.
//
// It is computed from explicit bytes and writes nothing, so the same plan backs
// a `--dry-run` and the transaction that applies it: what a preview shows and
// what an apply writes are one computation and cannot disagree.
//
// A plan that moves nothing is the normal outcome of running a migration twice.
// That is what makes the operation idempotent: the second run reports no change,
// writes no bytes, and records no history, because the first run left the vault
// in the shape this planner is trying to produce.
type MigrationPlan struct {
	// Root is the vault the plan was computed for.
	Root string
	// Moves lists every relocated key, sorted, in both directions.
	Moves []MigrationMove
	// Portable and Local are the rewrites of the two configuration files. A file
	// the migration leaves alone reports Changed false and no diff.
	Portable PatchResult
	Local    PatchResult
	// Sources holds the exact bytes the plan was computed from. They are the
	// transaction preconditions an apply must verify: a plan is only valid
	// against the files it read.
	Sources Sources
}

// Changed reports whether the plan would write anything.
func (p MigrationPlan) Changed() bool { return p.Portable.Changed || p.Local.Changed }

// MovedKeys returns just the key names, for a message that only has to name
// them.
func (p MigrationPlan) MovedKeys() []string {
	keys := make([]string, 0, len(p.Moves))
	for _, move := range p.Moves {
		keys = append(keys, move.Key)
	}
	return keys
}

// PlanMigration routes every assignment sitting in the file that does not own it
// into the file that does.
//
// Both directions are handled and both matter, for the reason checkOwnership
// gives: a portable key parked in the overlay never reaches the peer vault, and
// a local key left in the portable file does reach it and overwrites the peer's
// own editor, address, or agent command. The overwhelmingly common case is the
// second one, because every legacy single-file configuration is exactly that.
//
// Assignments are moved as *bytes*. The lines the source file holds are cut out
// and inserted into the destination unchanged, with only a dotted key respelled
// bare for the table it lands in, so a value this package never decodes is a
// value it cannot re-render wrongly — the user's quoting, spacing, and trailing
// comment all survive the move. Comments on the lines *above* an assignment stay
// where they are: nothing in a TOML file says which key a free-standing comment
// documents, and guessing would move text the user never asked to move.
//
// The result is verified before it is returned: the migrated pair must load, and
// every key the ownership table names must hold exactly the value it held
// before. A migration that would change what the vault does is a bug in this
// planner, and it is refused here rather than written.
func PlanMigration(root string, origin VaultOrigin, src Sources) (MigrationPlan, error) {
	if err := requireRoot(root); err != nil {
		return MigrationPlan{}, err
	}
	// Read the current meaning with ownership suspended. An enrolled vault whose
	// keys are misplaced is refused by the loader, and that vault is precisely
	// the one this command exists to repair: refusing to plan a migration
	// because the configuration is misplaced would leave the only fix behind the
	// fault it fixes. Every other way of being invalid still fails here.
	before, err := decodeSourcesAs(root, origin, src, EnrollmentNone)
	if err != nil {
		return MigrationPlan{}, fmt.Errorf(
			"config: this vault's configuration does not load, so it cannot be migrated; fix it first: %w", err)
	}

	moves, err := plannedMoves(root, src)
	if err != nil {
		return MigrationPlan{}, err
	}
	plan := MigrationPlan{Root: root, Moves: moves, Sources: src}
	if len(moves) == 0 {
		plan.Portable = unchangedPatch(src.Portable)
		plan.Local = unchangedPatch(src.Local)
		return plan, nil
	}

	portable, local, err := relocate(src, moves)
	if err != nil {
		return MigrationPlan{}, err
	}
	plan.Portable = portable
	plan.Local = local

	after, err := DecodeSources(root, origin, Sources{Portable: portable.Patched, Local: local.Patched})
	if err != nil {
		return MigrationPlan{}, fmt.Errorf("config: the migrated configuration would not load, so it was not written: %w", err)
	}
	if !sameEffectiveConfig(before, after) {
		return MigrationPlan{}, fmt.Errorf(
			"config: the migration would change what this vault does, so it was not written; move %s by hand",
			strings.Join(plan.MovedKeys(), ", "))
	}
	return plan, nil
}

// plannedMoves lists every key assigned in the file that does not own it.
func plannedMoves(root string, src Sources) ([]MigrationMove, error) {
	_, portableMeta, err := decodeLayer(layerPath(root, LayerPortable), src.Portable)
	if err != nil {
		return nil, err
	}
	assigned := map[Layer]map[string]bool{LayerPortable: assignedFields(portableMeta)}
	if src.LocalPresent() {
		_, localMeta, err := decodeLayer(layerPath(root, LayerLocal), src.Local)
		if err != nil {
			return nil, err
		}
		assigned[LayerLocal] = assignedFields(localMeta)
	}

	var moves []MigrationMove
	for _, spec := range fieldSpecs {
		for _, held := range []Layer{LayerPortable, LayerLocal} {
			if spec.layer != held && assigned[held][spec.key] {
				moves = append(moves, MigrationMove{
					Key: spec.key, From: held, To: spec.layer,
					Shadowed: assigned[LayerPortable][spec.key] && assigned[LayerLocal][spec.key],
				})
			}
		}
	}
	sort.Slice(moves, func(i, j int) bool { return moves[i].Key < moves[j].Key })
	return moves, nil
}

// relocate cuts each moved assignment out of the file holding it and inserts it
// into the file that owns it.
//
// Every move is applied against the working copy the previous one produced, so
// line numbers stay correct as the files shrink and grow. An assignment this
// cannot find as a whole line-level assignment is refused rather than skipped:
// the ownership check says the key is there, so failing to locate it means the
// file spells it in a form this mover does not handle, and dropping it silently
// would leave the key behind while reporting that it moved.
func relocate(src Sources, moves []MigrationMove) (PatchResult, PatchResult, error) {
	portableBefore, portableNewline := splitConfigLines(src.Portable)
	localBefore, localNewline := splitConfigLines(src.Local)
	files := map[Layer][]string{LayerPortable: portableBefore, LayerLocal: localBefore}
	if len(localBefore) == 0 {
		// The overlay is absent or empty and is about to receive a key, so this
		// call is creating it: it ends the way every other configuration file
		// ends, and it says why it exists. Only a portable-to-local move can
		// reach here, because a move *out* of the overlay needs the overlay to
		// have assigned something.
		localNewline = true
		files[LayerLocal] = append([]string(nil), localFileHeader...)
	}

	for _, move := range moves {
		if err := applyMove(files, move); err != nil {
			return PatchResult{}, PatchResult{}, err
		}
	}

	return patchResultFor(src.Portable, portableBefore, files[LayerPortable], portableNewline),
		patchResultFor(src.Local, localBefore, files[LayerLocal], localNewline),
		nil
}

// applyMove relocates one assignment between the two working copies.
func applyMove(files map[Layer][]string, move MigrationMove) error {
	table, leaf, ok := strings.Cut(move.Key, ".")
	if !ok {
		return fmt.Errorf(
			"config: %s is not a table key, so it cannot be relocated automatically; move it by hand", move.Key)
	}
	site := findKeySite(files[move.From], table, leaf)
	if !site.found() {
		return fmt.Errorf(
			"config: %s: %s is assigned in a form this migration cannot move; move it into %s by hand",
			move.From.FileName(), move.Key, move.To.FileName())
	}
	assignment := append([]string(nil), files[move.From][site.assign:site.assignEnd+1]...)
	if site.dotted {
		assignment[0] = rekeyAssignment(assignment[0], leaf)
	}
	files[move.From] = cutKeySite(files[move.From], site)
	if move.Shadowed {
		return resolveShadowedMove(files, move, table, leaf, assignment)
	}
	files[move.To] = insertKeyAssignment(files[move.To], table, assignment)
	return nil
}

// resolveShadowedMove finishes a move of a key that *both* files assigned.
//
// Only one of the two assignments was ever in force: the overlay wins wherever
// both files bind a key, so the portable file's copy is already dead text. The
// migration therefore keeps the value the vault is actually using and drops the
// other one — the only way to leave a single assignment behind without changing
// what the vault does. The discarded line shows up in the dry run and is
// recoverable through `bn undo`.
//
// Refusing instead is not available: `bn config set` already refuses a write the
// overlay would shadow and sends the user here, so a migration that also refused
// would leave that vault in a state no command can repair.
func resolveShadowedMove(files map[Layer][]string, move MigrationMove, table, leaf string, assignment []string) error {
	if move.To == LayerLocal {
		// The destination is the overlay, so the line just cut out of the portable
		// file is the dead one and the winning value is already in the file that
		// owns it. Nothing is inserted.
		return nil
	}
	dest := findKeySite(files[move.To], table, leaf)
	if !dest.found() {
		return fmt.Errorf(
			"config: %s: %s is assigned in a form this migration cannot replace; "+
				"remove one of the two assignments by hand and run this again",
			move.To.FileName(), move.Key)
	}
	files[move.To] = insertKeyAssignment(cutKeySite(files[move.To], dest), table, assignment)
	return nil
}

// patchResultFor renders one file's rewrite in the shape every other config
// patch reports.
func patchResultFor(source []byte, before, after []string, trailingNewline bool) PatchResult {
	patched := joinConfigLines(after, trailingNewline)
	result := PatchResult{Patched: patched, Changed: string(patched) != string(source)}
	if result.Changed {
		result.Diff = diffLines(before, after)
	}
	return result
}

// unchangedPatch is the no-op result for a file a plan does not touch.
func unchangedPatch(source []byte) PatchResult {
	return PatchResult{Patched: source}
}

// sameEffectiveConfig reports whether two loaded configurations hold the same
// value for every key the ownership table names.
//
// It compares through fieldSpecs rather than through the whole struct, because
// the whole struct also carries where the configuration came from — and where it
// came from is exactly what a migration changes. Driving the comparison from the
// same table that drives the merge is what keeps a newly added field covered
// here without anybody remembering to add it.
func sameEffectiveConfig(a, b *Config) bool {
	for _, spec := range fieldSpecs {
		var x, y Config
		spec.copy(&x, a)
		spec.copy(&y, b)
		if !reflect.DeepEqual(x, y) {
			return false
		}
	}
	return true
}
