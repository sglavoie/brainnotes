package cli

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/queryexpr"
	"github.com/sglavoie/brainnotes/internal/recall"
	"github.com/sglavoie/brainnotes/internal/vault"
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
	"bn rename": true,
}

// positionalCompleters complete the positional arguments of the commands whose
// arguments are not a single note: each completer is chosen by position. They
// live in completion_args.go.
var positionalCompleters = map[string]completer{
	"bn values":           positional(cobra.ShellCompDirectiveNoFileComp, vaultCompleter(frontmatterKeys)),
	"bn move":             positional(cobra.ShellCompDirectiveNoFileComp, vaultCompleter(noteTitles), vaultCompleter(folderNames)),
	"bn related feedback": positional(cobra.ShellCompDirectiveNoFileComp, vaultCompleter(noteTitles), vaultCompleter(noteTitles)),
	"bn views show":       positional(cobra.ShellCompDirectiveNoFileComp, vaultCompleter(viewNames)),
	"bn new":              positional(cobra.ShellCompDirectiveNoFileComp, vaultCompleter(templateNames)),
	"bn undo":             positional(cobra.ShellCompDirectiveNoFileComp, journalEntryCompleter),
	"bn restore":          positional(cobra.ShellCompDirectiveNoFileComp, backupArchiveCompleter),
	"bn config explain":   positional(cobra.ShellCompDirectiveNoFileComp, staticCompleter(configKeys())),
	"bn config get":       positional(cobra.ShellCompDirectiveNoFileComp, staticCompleter(scriptableConfigKeys())),
	"bn config set":       positional(cobra.ShellCompDirectiveNoFileComp, staticCompleter(scriptableConfigKeys())),
	"bn config unset":     positional(cobra.ShellCompDirectiveNoFileComp, staticCompleter(scriptableConfigKeys())),
	// The trailing [notes...] keep their path completion.
	"bn rename-key": positional(cobra.ShellCompDirectiveDefault, vaultCompleter(frontmatterKeys), noCompletion),
	"bn replace":    positional(cobra.ShellCompDirectiveDefault, vaultCompleter(frontmatterKeys), noCompletion, noCompletion),
}

// vaultFlagValues complete flags whose names are too generic to register on
// every command that defines them, by command path and flag name.
var vaultFlagValues = map[string]map[string]func(*config.Config, *vault.Vault) []string{
	"bn find":    {"relative-to": noteTitles},
	"bn capture": {"note": notePaths},
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
		for flag, names := range vaultFlagValues[path] {
			registerFlagValues(cmd, flag, vaultCompleter(names))
		}
		registerFlagValues(cmd, "view", vaultCompleter(viewNames))
		registerFlagValues(cmd, "tag", vaultCompleter(tagNames))
		registerFlagValues(cmd, "property", vaultCompleter(definedPropertyNames))
		registerFlagValues(cmd, "folder", vaultCompleter(folderNames))
		registerFlagValues(cmd, "has", vaultCompleter(frontmatterKeys))
		registerFlagValues(cmd, "missing", vaultCompleter(frontmatterKeys))
		if cmd.ValidArgsFunction != nil {
			return
		}
		if fn, ok := positionalCompleters[path]; ok {
			cmd.ValidArgsFunction = fn
		} else if noteArgCommands[path] {
			cmd.ValidArgsFunction = firstArgCompleter(vaultCompleter(noteTitles))
		}
	})
}

func walkCommands(cmd *cobra.Command, visit func(*cobra.Command)) {
	visit(cmd)
	for _, child := range cmd.Commands() {
		walkCommands(child, visit)
	}
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
		cfg, v, ok := loadCompletionVault(cmd)
		if !ok {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return names(cfg, v), cobra.ShellCompDirectiveNoFileComp
	}
}

// loadCompletionVault loads the configuration and vault the command would act
// on, reading only. ok is false when either does not load.
func loadCompletionVault(cmd *cobra.Command) (*config.Config, *vault.Vault, bool) {
	vaultFlag, _ := cmd.Flags().GetString("vault")
	cfg, err := config.Load(config.LoadOptions{VaultFlag: vaultFlag})
	if err != nil {
		return nil, nil, false
	}
	v, err := vault.Open(cfg)
	if err != nil {
		return nil, nil, false
	}
	return cfg, v, true
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
	counts, _ := collectValueCounts(v, notes, "tags", false, nil)
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
