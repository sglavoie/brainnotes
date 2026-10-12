package mutationmatrix

import (
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/cockpit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

// noteopsMutations are the shared mutation layer's own entry points, called
// directly the way both the CLI and the web APIs call them.
func noteopsMutations() []mutation {
	return []mutation{
		{
			name: "noteops/capture",
			apply: func(t *testing.T, h *harness, _ any) error {
				_, err := noteops.ApplyCapture(h.root, "Inbox.md", noteops.CaptureRequest{
					Heading: "## Captures",
					Text:    "a captured thought",
					At:      time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC),
				}, &h.cfg.Frontmatter, noteops.WithCaptureCommand("bn capture"))
				return err
			},
			touches:   []string{"Inbox.md"},
			journaled: true,
		},
		{
			name: "noteops/create",
			apply: func(t *testing.T, h *harness, _ any) error {
				_, err := noteops.CreateStubFromBrokenTarget(h.root, "Alpha.md", "Missing Note",
					noteops.WithCreateCommand("bn create-stub"))
				return err
			},
			touches:   []string{"Missing Note.md"},
			journaled: true,
		},
		{
			name: "noteops/frontmatter",
			apply: func(t *testing.T, h *harness, _ any) error {
				return setStatus(t, h, "Alpha.md", "done")
			},
			touches:   []string{"Alpha.md"},
			journaled: true,
			noop: func(t *testing.T, h *harness, _ any) error {
				// Alpha.md already carries status: draft, so this is the same code
				// path with nothing to write.
				return setStatus(t, h, "Alpha.md", "draft")
			},
		},
		{
			name: "noteops/bulk",
			apply: func(t *testing.T, h *harness, _ any) error {
				mutations, err := noteops.ParseFrontmatterMutations([]string{"status=done"}, "", h.cfg)
				if err != nil {
					return err
				}
				_, err = noteops.BulkSetFrontmatter(h.root, []noteops.BulkSource{
					{Key: "Alpha.md", RelPath: "Alpha.md"},
					{Key: "Notes/Gamma.md", RelPath: "Notes/Gamma.md"},
				}, mutations, &h.cfg.Frontmatter, noteops.WithBulkCommand("bn web bulk set"))
				return err
			},
			touches:   []string{"Alpha.md", "Notes/Gamma.md"},
			journaled: true,
		},
		{
			name: "noteops/trash",
			apply: func(t *testing.T, h *harness, _ any) error {
				_, err := noteops.TrashNote(h.root, "Alpha.md", noteops.WithTrashCommand("bn web trash"))
				return err
			},
			touches:   []string{"Alpha.md", "Trash/Alpha.md"},
			journaled: true,
		},
		{
			name: "noteops/rename",
			apply: func(t *testing.T, h *harness, _ any) error {
				_, err := noteops.RenameNote(h.v, h.cfg, h.abs("Alpha.md"), "Alpha Renamed",
					noteops.WithRenameCommand("bn rename Alpha.md 'Alpha Renamed'"))
				return err
			},
			touches:   []string{"Alpha Renamed.md", "Alpha.md"},
			journaled: true,
		},
	}
}

// cliMutations are commands driven through the real Cobra tree, from argv down.
func cliMutations() []mutation {
	return []mutation{
		{
			name: "cli/set",
			apply: func(t *testing.T, h *harness, _ any) error {
				return runCLI(t, h, "set", "Alpha.md", "status=done")
			},
			touches:   []string{"Alpha.md"},
			journaled: true,
			noop: func(t *testing.T, h *harness, _ any) error {
				return runCLI(t, h, "set", "Alpha.md", "status=draft")
			},
		},
		{
			name: "cli/config-set",
			prepare: func(t *testing.T, h *harness) any {
				// A stored floor is what makes the no-op below reachable: with no
				// assignment there is nothing for a set to already agree with.
				if err := runCLI(t, h, "config", "set", autosaveKey, "1m"); err != nil {
					t.Fatalf("prepare a stored autosave floor: %v", err)
				}
				return nil
			},
			apply: func(t *testing.T, h *harness, _ any) error {
				return runCLI(t, h, "config", "set", autosaveKey, "2m")
			},
			touches:   []string{config.FileName},
			journaled: true,
			noop: func(t *testing.T, h *harness, _ any) error {
				// 60s is the stored 1m spelled differently, so this is the same
				// code path with nothing to write.
				return runCLI(t, h, "config", "set", autosaveKey, "60s")
			},
		},
		{
			name: "cli/config-unset",
			prepare: func(t *testing.T, h *harness) any {
				if err := runCLI(t, h, "config", "set", autosaveKey, "1m"); err != nil {
					t.Fatalf("prepare a stored autosave floor: %v", err)
				}
				return nil
			},
			apply: func(t *testing.T, h *harness, _ any) error {
				return runCLI(t, h, "config", "unset", autosaveKey)
			},
			touches:   []string{config.FileName},
			journaled: true,
			// The no-op unset — a key that is not assigned — cannot be expressed
			// here, because this row's prepare is what puts the key there.
			// TestConfigMutationsWithNothingToDoLeaveNoHistory covers it.
		},
		{
			name: "cli/config-migrate",
			prepare: func(t *testing.T, h *harness) any {
				// A misplaced key, committed, so the row measures the migration and
				// not the edit that made one necessary. open.editor names this
				// machine, so the portable file is the wrong file for it — legal
				// here only because this vault is not enrolled.
				h.writeFile(config.FileName, fixtureConfig+"\n[open]\neditor = \"hx\"\n")
				h.rawGit("add", "-A")
				h.rawGit("commit", "-q", "-m", "misplace a machine-local key")
				return nil
			},
			apply: func(t *testing.T, h *harness, _ any) error {
				return runCLI(t, h, "config", "migrate")
			},
			// One commit, and only the portable half of the pair is in it. The
			// migration writes both files under one transaction, but
			// brainnotes.local.toml is out of the content history's scope by
			// design — it names this machine, and committing it would hand one
			// machine's settings to whatever restores from the remote. So the
			// vault's history records the departure and not the arrival, which is
			// the same thing it records for a key that was never misplaced.
			touches:   []string{config.FileName},
			journaled: true,
			// The no-op — a second migrate, or a vault that was never misplaced —
			// cannot live here, because this row's prepare is what creates the work.
			// TestConfigMigrateWithNothingToDoLeavesNoHistory covers it.
		},
	}
}

// surfaceMutations are the remaining production writers: the HTTP boundary,
// restore, and durable sidecar state.
func surfaceMutations() []mutation {
	return []mutation{
		{
			name: "web/capture",
			apply: func(t *testing.T, h *harness, _ any) error {
				return postCapture(t, h, "a captured thought")
			},
			touches:   []string{"Inbox.md"},
			journaled: true,
			overHTTP:  true,
		},
		{
			name: "restore/undo",
			prepare: func(t *testing.T, h *harness) any {
				result, err := noteops.SetFrontmatter(h.root, "Alpha.md", statusMutations(t, h, "done"),
					&h.cfg.Frontmatter, noteops.WithFrontmatterCommand("bn set Alpha.md status=done"))
				if err != nil {
					t.Fatalf("prepare an entry to undo: %v", err)
				}
				if result.Journal == nil {
					t.Fatal("the prepared mutation recorded no journal entry to undo")
				}
				return result.Journal.ID
			},
			apply: func(t *testing.T, h *harness, state any) error {
				_, err := noteops.Undo(h.root, state.(string), noteops.UndoOptions{Command: "bn undo"})
				return err
			},
			touches:   []string{"Alpha.md"},
			journaled: true,
		},
		{
			name: "vaultstate/cockpit",
			prepare: func(t *testing.T, h *harness) any {
				// Clearing an absent task is what creates the state file, so the
				// no-op below is a second write of bytes that are already there
				// rather than the file's first appearance.
				if err := cockpit.NewStateStore(h.root).Clear(cockpitTask); err != nil {
					t.Fatalf("prepare cockpit state: %v", err)
				}
				return nil
			},
			apply: func(t *testing.T, h *harness, _ any) error {
				return cockpit.NewStateStore(h.root).MarkDone(cockpitTask)
			},
			touches: []string{".bnotes/cockpit.json"},
			noop: func(t *testing.T, h *harness, _ any) error {
				return cockpit.NewStateStore(h.root).Clear(cockpitTask)
			},
		},
	}
}
