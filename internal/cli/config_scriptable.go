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

// readConfigSource reads the brainnotes.toml a patch is computed from.
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
			source, err := loadConfigSource(label, cfg)
			if err != nil {
				return err
			}
			// Returned unwrapped: the patcher's message already names the key and
			// the range it rejected the value against, and prefixing it with the
			// command would only spell "config" twice.
			result, err := config.PatchAutosaveMinInterval(source, args[1])
			if err != nil {
				return err
			}
			return applyConfigPatch(cmd, cfg, configPatch{
				label:     label,
				operation: configSetOperation,
				command:   commandInvocation(cmd, args),
				source:    source,
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
			source, err := loadConfigSource(label, cfg)
			if err != nil {
				return err
			}
			return applyConfigPatch(cmd, cfg, configPatch{
				label:     label,
				operation: configUnsetOperation,
				command:   commandInvocation(cmd, args),
				source:    source,
				result:    config.UnsetAutosaveMinInterval(source),
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

func loadConfigSource(label string, cfg *config.Config) ([]byte, error) {
	source, err := readConfigSource(cfg.SourcePath)
	if err != nil {
		return nil, fmt.Errorf("%s: read %s: %w", label, cfg.SourcePath, err)
	}
	return source, nil
}

// configPatch is one planned rewrite of brainnotes.toml: the bytes it was
// computed from, the candidate it produced, and the fixed tokens the resulting
// snapshot and undo entry are recorded under.
type configPatch struct {
	label     string
	operation string
	command   string
	source    []byte
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
		return writeConfigPatchDiff(cmd.OutOrStdout(), patch.result.Diff)
	}
	// The patcher validates the value it writes, so this cannot normally fire.
	// It is here because the candidate is a whole file and the patcher only
	// vouches for one line of it: whatever else the source picked up between the
	// load at startup and the read just now is in these bytes too, and a
	// configuration the vault could not start from is not something to leave
	// behind.
	if _, err := config.Decode(cfg.Vault, cfg.VaultOrigin, patch.result.Patched); err != nil {
		return fmt.Errorf("%s: the patched %s would not load, so it was not written: %w",
			patch.label, config.FileName, err)
	}

	journaler, err := noteops.BeginOperation(cfg.Vault, patch.operation, patch.command, []string{cfg.SourcePath})
	if err != nil {
		return fmt.Errorf("%s: %w", patch.label, err)
	}
	txn, err := vault.NewFileTxn(cfg.Vault)
	if err != nil {
		return fmt.Errorf("%s: open transaction: %w", patch.label, err)
	}
	// The expectation is the whole point of reading the source rather than
	// re-rendering the loaded Config: a patch is only valid against the bytes it
	// was computed from, and anything else on that path is another writer's.
	if err := txn.Write(cfg.SourcePath, patch.result.Patched, 0, vault.ExpectHash(vault.HashBytes(patch.source))); err != nil {
		return configTxnError(patch.label, err)
	}
	if beforeConfigCommit != nil {
		beforeConfigCommit()
	}
	if err := txn.Commit(journaler); err != nil {
		return configTxnError(patch.label, err)
	}
	if _, err := journaler.Finalize(); err != nil {
		return fmt.Errorf("%s: %w", patch.label, err)
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
func configTxnError(label string, err error) error {
	if vault.IsTxnConflict(err) {
		return fmt.Errorf("%s: %s changed on disk while the command was running, so nothing was written; re-run it: %w",
			label, config.FileName, err)
	}
	return fmt.Errorf("%s: %w", label, err)
}

// writeConfigPatchDiff prints the patch helper's own diff under the same
// `--- <relpath>` header every other dry run uses. The lines are the patcher's,
// not a re-derivation: what a dry run shows and what an apply would write come
// from one computation, so they cannot disagree.
func writeConfigPatchDiff(out io.Writer, diff []config.PatchLine) error {
	if _, err := fmt.Fprintf(out, "--- %s\n", config.FileName); err != nil {
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
