package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// configScriptableKey is the one configuration key `bn config get`, `set`, and
// `unset` accept.
//
// The surface is a fixed key rather than a generic path walker on purpose.
// Every key it could grow has to answer three questions first — what its
// normalized spelling is, what an absent assignment falls back to, and whether
// rewriting it under a running watcher is safe — and `autosave.min_interval` is
// currently the only one with all three answers. Anything else is refused by
// name so a script never half-works against a key nothing validates.
const configScriptableKey = "autosave.min_interval"

// configSetOperation and configUnsetOperation are the fixed tokens these two
// mutations snapshot under. They are what reaches the commit message, so they
// are chosen here and never derived from the value the user passed.
const (
	configSetOperation   = "config-set"
	configUnsetOperation = "config-unset"
)

// readConfigSource reads one configuration layer a patch is computed from.
//
// It is a variable because two failures this command has to survive can only be
// produced by another writer landing in the microseconds between the read and
// the transaction: a candidate whose patched form would not load, and a source
// the transaction's expected hash no longer matches. Racing a second process
// cannot test either one reliably; substituting the read can. Production always
// reads the file.
var readConfigSource = os.ReadFile

func newConfigGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get KEY",
		Short: "Print one configuration value in its normalized form",
		Long: "Print the effective value of a configuration key, normalized the way `bn` writes it: `bn config get " +
			configScriptableKey + "` prints `5m` whether the file says `5m`, `300s`, or nothing at all.\n\n" +
			"The output is the value and a newline, so it can be read straight into a shell variable. " +
			configScriptableKey + " is currently the only scriptable key.",
		Args:          configArgs("get", 1, "bn config get "+configScriptableKey),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := configForKey("config get", cmd, args[0])
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), cfg.AutosavePolicy().Canonical)
			return err
		},
	}
}

func newConfigSetCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "set KEY VALUE",
		Short: "Set one configuration value in the vault's brainnotes.toml",
		Long: "Set a configuration key in the vault's brainnotes.toml, rewriting exactly that one assignment and leaving every other byte — including comments — where it was.\n\n" +
			"The value is normalized before it is written, so `300s` is stored as `5m`. Setting a value that names the " +
			"policy already in force changes nothing and records nothing — whatever spelling the file uses, and " +
			"including the default over a file that assigns nothing — so a script may reassert a policy on every run " +
			"without producing a commit on every run. To remove the assignment itself, use `bn config unset`.\n\n" +
			configScriptableKey + " is currently the only scriptable key. It takes `off` or a Go duration between " +
			config.CanonicalAutosaveDuration(config.MinAutosaveInterval) + " and " +
			config.CanonicalAutosaveDuration(config.MaxAutosaveInterval) + " inclusive.",
		Args:          configArgs("set", 2, "bn config set "+configScriptableKey+" "+config.DefaultAutosaveMinInterval),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			const label = "config set"
			cfg, err := configForKey(label, cmd, args[0])
			if err != nil {
				return err
			}
			target, err := resolveConfigTarget(label, cfg, args[0])
			if err != nil {
				return err
			}
			// Returned unwrapped: the patcher's message already names the key and
			// the range it rejected the value against, and prefixing it with the
			// command would only spell "config" twice.
			result, err := config.PatchAutosaveMinInterval(target.source(), args[1])
			if err != nil {
				return err
			}
			return applyConfigPatch(cmd, cfg, configPatch{
				label:     label,
				operation: configSetOperation,
				command:   commandInvocation(cmd, args),
				target:    target,
				result:    result,
				dryRun:    dryRun,
			})
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the change without writing "+config.FileName)
	return cmd
}

func newConfigUnsetCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "unset KEY",
		Short: "Remove one configuration assignment, restoring its default",
		Long: "Remove a key's assignment from the vault's brainnotes.toml so the built-in default applies again. " +
			"Unsetting `" + configScriptableKey + "` restores the " + config.DefaultAutosaveMinInterval + " floor.\n\n" +
			"The table the key lived in and every comment around it stay exactly where they were. Unsetting a key that " +
			"is not assigned changes nothing and records nothing.",
		Args:          configArgs("unset", 1, "bn config unset "+configScriptableKey),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			const label = "config unset"
			cfg, err := configForKey(label, cmd, args[0])
			if err != nil {
				return err
			}
			target, err := resolveConfigTarget(label, cfg, args[0])
			if err != nil {
				return err
			}
			return applyConfigPatch(cmd, cfg, configPatch{
				label:     label,
				operation: configUnsetOperation,
				command:   commandInvocation(cmd, args),
				target:    target,
				result:    config.UnsetAutosaveMinInterval(target.source()),
				dryRun:    dryRun,
			})
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the change without writing "+config.FileName)
	return cmd
}

// configArgs is the arity check for a scriptable config command. Cobra's own
// ExactArgs reports "accepts 2 arg(s), received 1", which says nothing about
// what the missing one was; a script's author needs the shape of the call, so
// that is what this prints.
func configArgs(name string, want int, usage string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) == want {
			return nil
		}
		return fmt.Errorf("config %s: expected %d argument(s), got %d; usage: %s", name, want, len(args), usage)
	}
}

// configForKey resolves the loaded configuration and rejects any key other than
// the one scriptable one, by name, before the command does anything else.
func configForKey(label string, cmd *cobra.Command, key string) (*config.Config, error) {
	if key != configScriptableKey {
		return nil, fmt.Errorf("%s: unsupported key %q; the only scriptable key is %s", label, key, configScriptableKey)
	}
	cfg := ConfigFromContext(cmd.Context())
	if cfg == nil {
		return nil, errors.New(label + ": no configuration loaded")
	}
	return cfg, nil
}

// configTarget is the file one scriptable write lands in, together with the
// bytes every configuration layer held when the patch was computed.
//
// The destination is decided by the ownership table and not by where the value
// currently comes from. A key no file assigns still has an owner, which is the
// case a first write is always in; routing by provenance would make the same
// command write a different file on a different machine.
type configTarget struct {
	// root is the vault both layers live at the top of, so the file this write
	// does *not* touch can still be named as a precondition.
	root string
	// layer and path name the file that owns the key being written.
	layer config.Layer
	path  string
	// sources holds both layers as they were read. A patch is computed from one
	// file and validated against the pair, because what the vault loads is never
	// one file on its own.
	sources config.Sources
	// exists records whether the target file was on disk. An absent overlay is a
	// state of its own, and the transaction must expect the state it read.
	exists bool
}

// source returns the bytes of the layer being written.
func (t configTarget) source() []byte {
	if t.layer == config.LayerLocal {
		return t.sources.Local
	}
	return t.sources.Portable
}

// candidate returns both layers with the patched bytes substituted for the
// target's, which is what the vault would load if this write landed.
func (t configTarget) candidate(patched []byte) config.Sources {
	out := t.sources
	if t.layer == config.LayerLocal {
		out.Local = patched
		return out
	}
	out.Portable = patched
	return out
}

// expectation is the transaction precondition this write needs: the exact bytes
// the patch was computed from, or the file's absence when it had none. An
// overlay that appeared between the read and the commit is another writer's,
// and ExpectMissing is what turns that into a refusal instead of a clobber.
func (t configTarget) expectation() vault.FileExpectation {
	if !t.exists {
		return vault.ExpectMissing()
	}
	return vault.ExpectHash(vault.HashBytes(t.source()))
}

// otherLayer names the file this write does not touch.
func (t configTarget) otherLayer() config.Layer {
	if t.layer == config.LayerLocal {
		return config.LayerPortable
	}
	return config.LayerLocal
}

// otherPath is where that file lives.
func (t configTarget) otherPath() string {
	return config.LayerPath(t.root, t.otherLayer())
}

// otherExpectation pins the layer this write does not touch.
//
// The patch is only correct because of what the *pair* said: whether the write
// is shadowed, whether it is a semantic no-op, and whether the result loads at
// all are each decided by reading both files. Writing the target after the other
// layer moved would be a write nothing validated — and in the shadowing case a
// write with no effect, reported as a success, which is the one outcome this
// command refuses everywhere else. An absent overlay is required to stay absent
// for the same reason: one that appeared could assign the very key being
// written.
func (t configTarget) otherExpectation() vault.FileExpectation {
	if t.otherLayer() == config.LayerLocal && !t.sources.LocalPresent() {
		return vault.ExpectMissing()
	}
	if t.otherLayer() == config.LayerLocal {
		return vault.ExpectHash(vault.HashBytes(t.sources.Local))
	}
	return vault.ExpectHash(vault.HashBytes(t.sources.Portable))
}

// resolveConfigTarget decides which file a write to key belongs in and reads
// every layer it will be validated against.
func resolveConfigTarget(label string, cfg *config.Config, key string) (configTarget, error) {
	path, ok := cfg.OwnerPath(key)
	if !ok {
		return configTarget{}, fmt.Errorf("%s: %s names no configuration field", label, key)
	}
	layer, _ := config.KeyLayer(key)
	if err := refuseShadowedWrite(label, cfg, key, layer); err != nil {
		return configTarget{}, err
	}
	sources, err := readConfigSources(label, cfg.Vault)
	if err != nil {
		return configTarget{}, err
	}
	return configTarget{
		root:    cfg.Vault,
		layer:   layer,
		path:    path,
		sources: sources,
		// The portable file is the vault-root marker, so it is always on disk;
		// only the overlay can be absent.
		exists: layer != config.LayerLocal || sources.LocalPresent(),
	}, nil
}

// readConfigSources reads every configuration layer through the same seam, so
// an absent overlay stays distinguishable from an unreadable one.
func readConfigSources(label, root string) (config.Sources, error) {
	portablePath := config.LayerPath(root, config.LayerPortable)
	portable, err := readConfigSource(portablePath)
	if err != nil {
		return config.Sources{}, fmt.Errorf("%s: read %s: %w", label, portablePath, err)
	}
	src := config.Sources{Portable: portable}

	localPath := config.LayerPath(root, config.LayerLocal)
	switch local, err := readConfigSource(localPath); {
	case err == nil:
		if local == nil {
			// Everywhere else an empty file reads as non-nil empty bytes; a nil
			// here would read as "no overlay", which is a different vault.
			local = []byte{}
		}
		src.Local = local
	case !os.IsNotExist(err):
		return config.Sources{}, fmt.Errorf("%s: read %s: %w", label, localPath, err)
	}
	return src, nil
}

// refuseShadowedWrite stops a write whose result the vault would not use.
//
// The overlay wins wherever both files assign a key, so writing the portable
// file for a key the overlay assigns produces a correct file and no change in
// behaviour — the worst shape a configuration command can have, because it
// reports success while the value the user just set is not the one in force.
// The other direction needs no guard: a write to the overlay is the one that
// wins, so it takes effect immediately.
func refuseShadowedWrite(label string, cfg *config.Config, key string, layer config.Layer) error {
	if layer != config.LayerPortable {
		return nil
	}
	if source, ok := cfg.SourceOf(key); !ok || source != config.LayerLocal {
		return nil
	}
	return fmt.Errorf(
		"%s: %s assigns %s and the overlay wins, so writing %s would not change what this vault does; "+
			"remove it from %s or run `bn config migrate`",
		label, config.LocalFileName, key, config.FileName, config.LocalFileName)
}

// configPatch is one planned rewrite of a configuration file: the file it lands
// in, the candidate it produced, and the fixed tokens the resulting snapshot and
// undo entry are recorded under.
type configPatch struct {
	label     string
	operation string
	command   string
	target    configTarget
	result    config.PatchResult
	dryRun    bool
}

// applyConfigPatch turns a planned rewrite into a durable one.
//
// The three cheap answers come first and each one returns before any artifact
// could exist: a patch that changes nothing is a success that writes nothing, a
// dry run prints the patch's own diff and stops, and a candidate that would not
// load is refused while it is still only bytes in memory. "Changes nothing" is
// the patcher's judgement and it is a policy-level one — a set whose value
// names the policy already in force reports no change, so the dry run below
// prints nothing for it too, and neither answer depends on how the file
// happens to spell what it already means. Only past those does
// the operation begin, and from there it is the same lifecycle every other
// mutation uses — the preflight ahead of the first artifact, one transaction
// pinned to the bytes the patch was computed from, and a finalize that writes
// the undo entry and the immediate history commit together.
func applyConfigPatch(cmd *cobra.Command, cfg *config.Config, patch configPatch) error {
	if !patch.result.Changed {
		return nil
	}
	if patch.dryRun {
		return writeConfigPatchDiff(cmd.OutOrStdout(), patch.target.layer.FileName(), patch.result.Diff)
	}
	// The patcher validates the value it writes, so this cannot normally fire.
	// It is here because the candidate is a whole file and the patcher only
	// vouches for one line of it: whatever else the source picked up between the
	// load at startup and the read just now is in these bytes too, and a
	// configuration the vault could not start from is not something to leave
	// behind.
	//
	// Both layers are decoded, not just the one being written. A candidate that
	// loads on its own can still be refused by the pair — a key the other file
	// owns, or an overlay that changed since startup — and the vault loads the
	// pair.
	if _, err := config.DecodeSources(cfg.Vault, cfg.VaultOrigin, patch.target.candidate(patch.result.Patched)); err != nil {
		return fmt.Errorf("%s: the patched %s would not load, so it was not written: %w",
			patch.label, patch.target.layer.FileName(), err)
	}

	journaler, err := noteops.BeginOperation(cfg.Vault, patch.operation, patch.command, []string{patch.target.path})
	if err != nil {
		return fmt.Errorf("%s: %w", patch.label, err)
	}
	defer journaler.Release()
	txn, err := vault.NewFileTxn(cfg.Vault)
	if err != nil {
		return fmt.Errorf("%s: open transaction: %w", patch.label, err)
	}
	if err := stageConfigPatch(txn, patch); err != nil {
		return err
	}
	if beforeConfigCommit != nil {
		beforeConfigCommit()
	}
	if err := txn.Commit(journaler); err != nil {
		// Either file can lose the race here — one was written, the other was
		// required — and the transaction names the one that did, so guessing a
		// file name in the prefix would only contradict it.
		return configTxnError(patch.label, "the configuration", err)
	}
	if _, err := journaler.Finalize(); err != nil {
		return fmt.Errorf("%s: %w", patch.label, err)
	}
	return nil
}

// stageConfigPatch stages the file the patch lands in and pins the one it does
// not touch.
//
// The other layer is required rather than written: the decision to write these
// bytes was made by reading the pair — which file owns the key, and whether the
// overlay shadows it — so the pair is what the commit has to still agree with.
// An overlay appearing under a portable write is the case this catches, and it
// is the one that would otherwise report a success nothing acts on.
func stageConfigPatch(txn *vault.FileTxn, patch configPatch) error {
	// The expectation is the whole point of reading the source rather than
	// re-rendering the loaded Config: a patch is only valid against the bytes it
	// was computed from, and anything else on that path is another writer's.
	if err := txn.Write(patch.target.path, patch.result.Patched, 0, patch.target.expectation()); err != nil {
		return configTxnError(patch.label, patch.target.layer.FileName(), err)
	}
	if err := txn.Require(patch.target.otherPath(), patch.target.otherExpectation()); err != nil {
		return configTxnError(patch.label, patch.target.otherLayer().FileName(), err)
	}
	return nil
}

// beforeConfigCommit runs between staging the candidate and committing it. Like
// readConfigSource it exists for one test: the concurrent writer whose edit has
// to become a refusal rather than an overwrite.
var beforeConfigCommit func()

// configTxnError keeps the two transaction failures a person can act on out of
// the generic wrapper. A stale precondition means someone else wrote the file,
// which is fixed by running the command again; everything else is passed
// through so the classified codes underneath it survive.
func configTxnError(label, file string, err error) error {
	if vault.IsTxnConflict(err) {
		return fmt.Errorf("%s: %s changed on disk while the command was running, so nothing was written; re-run it: %w",
			label, file, err)
	}
	return fmt.Errorf("%s: %w", label, err)
}

// writeConfigPatchDiff prints the patch helper's own diff under the same
// `--- <relpath>` header every other dry run uses. The lines are the patcher's,
// not a re-derivation: what a dry run shows and what an apply would write come
// from one computation, so they cannot disagree.
func writeConfigPatchDiff(out io.Writer, file string, diff []config.PatchLine) error {
	if _, err := fmt.Fprintf(out, "--- %s\n", file); err != nil {
		return err
	}
	for _, line := range diff {
		// A CRLF file's lines each carry their own carriage return. It is content
		// on disk and not something to print into a terminal.
		if _, err := fmt.Fprintf(out, "%s %d: %s\n", line.Kind, line.Line, strings.TrimSuffix(line.Text, "\r")); err != nil {
			return err
		}
	}
	return nil
}
