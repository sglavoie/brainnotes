package cli

import (
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/queryexpr"
	"github.com/sglavoie/sglavoie_brainnotes/internal/recall"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// Shell completion. `bn completion <shell>` is cobra's; what is registered
// here is what it completes beyond command and flag names: the fixed values a
// flag accepts, and the names that only the vault knows — note titles, saved
// views, tags, properties and folders.
//
// Completion runs as cobra's hidden __complete command, which skips
// PersistentPreRunE (skipsConfigLoad), so the vault-backed completers load the
// configuration themselves. A vault that does not load completes nothing
// rather than printing an error into the user's shell.

// enumFlagValues lists the fixed values of each enumerated flag, by command
// path and flag name. Each list must match the command's own validation.
var enumFlagValues = map[string]map[string][]string{
	"bn context":          {"format": {"markdown", "json"}, "purpose": recallPurposes()},
	"bn graph":            {"format": {"dot", "mermaid", "code"}},
	"bn related":          {"mode": {"balanced", "rediscover", "connect"}},
	"bn related feedback": {"state": {"useful", "dismissed", "later", "clear"}},
	"bn set":              {"value-type": {"date", "datetime", "int", "bool", "float", "raw"}},
	"bn tags":             {"sort": {"count", "name"}},
	"bn values":           {"sort": {"count", "name"}},
	"bn find":             {"sort": findSortCandidates()},
}

// noteArgCommands complete their first positional argument with note titles.
var noteArgCommands = map[string]bool{
	"bn backlinks": true, "bn context": true, "bn links": true, "bn open": true,
	"bn pin": true, "bn unpin": true, "bn related": true, "bn remove": true,
	"bn move": true, "bn rename": true,
}

func recallPurposes() []string {
	return []string{recall.PurposeSynthesis, recall.PurposeContradictions, recall.PurposeQuestions}
}

func findSortCandidates() []string {
	keys := queryexpr.SortKeys()
	out := make([]string, 0, 2*len(keys))
	for _, k := range keys {
		out = append(out, k, "-"+k)
	}
	return out
}

// registerCompletions walks the finished command tree once. It is called after
// every command is added, so a command added later is still reached.
func registerCompletions(root *cobra.Command) {
	walkCommands(root, func(cmd *cobra.Command) {
		path := cmd.CommandPath()
		for flag, values := range enumFlagValues[path] {
			registerFlagValues(cmd, flag, staticCompleter(values))
		}
		registerFlagValues(cmd, "view", vaultCompleter(viewNames))
		registerFlagValues(cmd, "tag", vaultCompleter(tagNames))
		registerFlagValues(cmd, "property", vaultCompleter(definedPropertyNames))
		registerFlagValues(cmd, "folder", vaultCompleter(folderNames))
		if noteArgCommands[path] && cmd.ValidArgsFunction == nil {
			cmd.ValidArgsFunction = firstArgCompleter(vaultCompleter(noteTitles))
		}
	})
	if values := findCommand(root, "values"); values != nil && values.ValidArgsFunction == nil {
		values.ValidArgsFunction = firstArgCompleter(vaultCompleter(frontmatterKeys))
	}
}

func walkCommands(cmd *cobra.Command, visit func(*cobra.Command)) {
	visit(cmd)
	for _, child := range cmd.Commands() {
		walkCommands(child, visit)
	}
}

func findCommand(root *cobra.Command, name string) *cobra.Command {
	for _, child := range root.Commands() {
		if child.Name() == name {
			return child
		}
	}
	return nil
}

type completer = func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective)

// registerFlagValues attaches fn to the flag when cmd defines it locally. An
// inherited flag is registered on the command that owns it.
func registerFlagValues(cmd *cobra.Command, flag string, fn completer) {
	if cmd.LocalNonPersistentFlags().Lookup(flag) == nil {
		return
	}
	// The only error is "already registered", which leaves the first one.
	_ = cmd.RegisterFlagCompletionFunc(flag, fn)
}

func staticCompleter(values []string) completer {
	return func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
		return values, cobra.ShellCompDirectiveNoFileComp
	}
}

func firstArgCompleter(fn completer) completer {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return fn(cmd, args, toComplete)
	}
}

// vaultCompleter loads the vault the command would act on and lists names from
// it. Every failure completes nothing.
func vaultCompleter(names func(*config.Config, *vault.Vault) []string) completer {
	return func(cmd *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
		vaultFlag, _ := cmd.Flags().GetString("vault")
		cfg, err := config.Load(config.LoadOptions{VaultFlag: vaultFlag})
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		v, err := vault.Open(cfg)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return names(cfg, v), cobra.ShellCompDirectiveNoFileComp
	}
}

func viewNames(cfg *config.Config, _ *vault.Vault) []string {
	names := make([]string, 0, len(cfg.Views))
	for _, view := range cfg.Views {
		names = append(names, view.Name)
	}
	sort.Strings(names)
	return names
}

func definedPropertyNames(cfg *config.Config, _ *vault.Vault) []string {
	names := make([]string, 0, len(cfg.Frontmatter.Properties))
	for name := range cfg.Frontmatter.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func frontmatterKeys(_ *config.Config, v *vault.Vault) []string {
	keys, err := collectPropertyKeys(v)
	if err != nil {
		return nil
	}
	return keys
}

func tagNames(_ *config.Config, v *vault.Vault) []string {
	notes, _, err := v.NotesLenient()
	if err != nil {
		return nil
	}
	counts, _ := collectValueCounts(v, notes, "tags", false, false, io.Discard)
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// noteTitles completes titles rather than paths: a title is what every
// note-taking command resolves first, and it is what the user remembers.
func noteTitles(_ *config.Config, v *vault.Vault) []string {
	paths, err := collectNotePaths(v)
	if err != nil {
		return nil
	}
	titles := make([]string, 0, len(paths))
	for _, p := range paths {
		titles = append(titles, (&vault.Note{Path: p}).Title())
	}
	sort.Strings(titles)
	return titles
}

// folderNames lists the vault-relative folders that hold at least one note.
func folderNames(_ *config.Config, v *vault.Vault) []string {
	paths, err := collectNotePaths(v)
	if err != nil {
		return nil
	}
	seen := make(map[string]struct{})
	for _, p := range paths {
		rel, err := filepath.Rel(v.Root, filepath.Dir(p))
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			continue
		}
		for dir := filepath.ToSlash(rel); dir != "."; dir = filepath.ToSlash(filepath.Dir(filepath.FromSlash(dir))) {
			seen[dir] = struct{}{}
		}
	}
	folders := make([]string, 0, len(seen))
	for dir := range seen {
		folders = append(folders, dir)
	}
	sort.Strings(folders)
	return folders
}
