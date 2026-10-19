package cli

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	tmpl "github.com/sglavoie/sglavoie_brainnotes/internal/template"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// Positional-argument completers that need more than a note title: the
// position-aware dispatcher, and the names only the vault's own directories
// know (templates, journal entries, backup archives) or only the
// configuration schema knows (keys). Every one of them reads and never writes.

// backupsDirName is the vault-root directory `bn backup` writes archives to.
const backupsDirName = "backups"

// positional completes argument i with byPos[i]. Past the last completer it
// returns rest, so a trailing [notes...] list can keep path completion.
func positional(rest cobra.ShellCompDirective, byPos ...completer) completer {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) >= len(byPos) {
			return nil, rest
		}
		return byPos[len(args)](cmd, args, toComplete)
	}
}

// noCompletion holds a free-form argument's place: nothing to offer, and no
// file names either.
func noCompletion(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return nil, cobra.ShellCompDirectiveNoFileComp
}

// configKeys lists every key `bn config explain` accepts.
func configKeys() []string {
	keys := append(config.LayerKeys(config.LayerPortable), config.LayerKeys(config.LayerLocal)...)
	sort.Strings(keys)
	return keys
}

// scriptableConfigKeys lists the keys `bn config get`, `set` and `unset` accept.
func scriptableConfigKeys() []string {
	return []string{configScriptableKey}
}

// notePaths lists notes as vault-relative paths, the form `capture --note`
// takes.
func notePaths(_ *config.Config, v *vault.Vault) []string {
	paths, err := collectNotePaths(v)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if rel, err := filepath.Rel(v.Root, p); err == nil && !strings.HasPrefix(rel, "..") {
			out = append(out, filepath.ToSlash(rel))
		}
	}
	sort.Strings(out)
	return out
}

// templateNames lists the shortest name `bn new` resolves to each template:
// "Book Template.md" completes as "Book". A name is offered only when
// template.Find resolves it back to the same file.
func templateNames(cfg *config.Config, _ *vault.Vault) []string {
	dir := cfg.Templates.Dir
	if dir == "" {
		dir = "Templates"
	}
	entries, err := os.ReadDir(filepath.Join(cfg.Vault, filepath.FromSlash(dir)))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if name := shortestTemplateName(cfg, e.Name()); name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func shortestTemplateName(cfg *config.Config, file string) string {
	stem := strings.TrimSuffix(file, filepath.Ext(file))
	for _, name := range []string{strings.TrimSuffix(stem, " Template"), stem, file} {
		if name == "" {
			continue
		}
		if found, err := tmpl.Find(cfg, name); err == nil && filepath.Base(found) == file {
			return name
		}
	}
	return ""
}

// journalEntryCompleter offers undo journal IDs newest first, the order
// `bn history` prints, each described by the command that recorded it.
func journalEntryCompleter(cmd *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	cfg, _, ok := loadCompletionVault(cmd)
	if !ok {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	entries, _, err := journal.ListEntries(journalRootForVault(cfg.Vault))
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	out := make([]cobra.Completion, 0, len(entries))
	for _, e := range entries {
		out = append(out, cobra.CompletionWithDesc(e.ID, e.Command))
	}
	return out, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveKeepOrder
}

// backupArchiveCompleter offers the archives under backups/, newest first, as
// the vault-relative paths `bn restore` resolves. With none to offer the shell
// falls back to file names, since restore also takes an archive elsewhere.
func backupArchiveCompleter(cmd *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	cfg, _, ok := loadCompletionVault(cmd)
	if !ok {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	entries, err := os.ReadDir(filepath.Join(cfg.Vault, backupsDirName))
	if err != nil {
		return nil, cobra.ShellCompDirectiveDefault
	}
	var out []cobra.Completion
	for _, e := range entries {
		name := e.Name()
		if e.Type().IsRegular() && !strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".tar.gz") {
			out = append(out, backupsDirName+"/"+name)
		}
	}
	if len(out) == 0 {
		return nil, cobra.ShellCompDirectiveDefault
	}
	// Archive names carry a UTC timestamp, so reverse name order is newest first.
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveKeepOrder
}
