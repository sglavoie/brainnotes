package config

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func validate(cfg *Config) error {
	if err := validateTemplatesDir(cfg.Templates.Dir); err != nil {
		return err
	}
	if err := validateProperties(cfg); err != nil {
		return err
	}
	if err := validateRoutes(cfg); err != nil {
		return err
	}
	if err := validateAgent(cfg); err != nil {
		return err
	}
	if err := validateBrief(cfg); err != nil {
		return err
	}
	if err := validateCapture(cfg); err != nil {
		return err
	}
	if err := validateCockpit(cfg); err != nil {
		return err
	}
	if err := validateViews(cfg); err != nil {
		return err
	}
	if err := validateAutosave(cfg); err != nil {
		return err
	}
	return nil
}

// validateAutosave holds the stored floor to exactly the rule
// ParseAutosaveMinInterval enforces, so a config that loads can never resolve
// to a policy the watcher would refuse.
func validateAutosave(cfg *Config) error {
	_, err := ParseAutosaveMinInterval(cfg.Autosave.MinInterval)
	return err
}

// savedViewNamePattern constrains a view name to something a shell argument,
// a URL fragment, and a nav label can all carry: letters, digits, spaces, and
// the three separators, never leading or trailing. Letter and digit classes are
// Unicode-aware because vault content in this repo routinely is.
var savedViewNamePattern = regexp.MustCompile(`^[\p{L}\p{N}](?:[\p{L}\p{N} ._-]*[\p{L}\p{N}])?$`)

// validateViews checks the [[views]] entries for shape only.
//
// It deliberately does not check that Query parses, that Sort names a real sort
// key, or that RelativeTo resolves to a note: internal/config sits below
// internal/queryexpr and cannot import it (see SavedView's documentation). A
// view whose expression rotted therefore loads without complaint and is
// surfaced as disabled-with-its-parse-error by the layer above, rather than
// making the whole vault unloadable.
func validateViews(cfg *Config) error {
	for i, view := range cfg.Views {
		if err := validateSavedView(fmt.Sprintf("views[%d]", i), view); err != nil {
			return err
		}
	}
	for i := 0; i < len(cfg.Views); i++ {
		for j := i + 1; j < len(cfg.Views); j++ {
			if strings.EqualFold(cfg.Views[i].Name, cfg.Views[j].Name) {
				return fmt.Errorf(
					"config: views: names %q and %q collide case-insensitively",
					cfg.Views[i].Name, cfg.Views[j].Name)
			}
		}
	}
	return nil
}

// validateSavedView reports whether one view is structurally usable. label
// names the view in the error — "views[2]" when loading a file, "view" when
// checking a patch that has no index yet.
func validateSavedView(label string, view SavedView) error {
	if err := validateSavedViewName(label, view.Name); err != nil {
		return err
	}
	if strings.TrimSpace(view.Query) == "" {
		return fmt.Errorf("config: %s (%q): query must not be empty or whitespace", label, view.Name)
	}
	if containsControlRune(view.Query) {
		return fmt.Errorf("config: %s (%q): query contains a control character", label, view.Name)
	}
	if containsControlRune(view.Sort) {
		return fmt.Errorf("config: %s (%q): sort contains a control character", label, view.Name)
	}
	if containsControlRune(view.RelativeTo) {
		return fmt.Errorf("config: %s (%q): relative_to contains a control character", label, view.Name)
	}
	if containsControlRune(view.Description) {
		return fmt.Errorf("config: %s (%q): description contains a control character", label, view.Name)
	}
	if view.Limit < 0 {
		return fmt.Errorf("config: %s (%q): limit %d must not be negative", label, view.Name, view.Limit)
	}
	return nil
}

// validateSavedViewName is split out so a rename patch, which carries a name
// and nothing else, checks the name by exactly the rule a loaded view is held
// to rather than by a second copy of it.
func validateSavedViewName(label, name string) error {
	if name == "" {
		return fmt.Errorf("config: %s: name is required", label)
	}
	if strings.TrimSpace(name) != name {
		return fmt.Errorf("config: %s: name %q must not have surrounding whitespace", label, name)
	}
	if containsControlRune(name) {
		return fmt.Errorf("config: %s: name %q contains a control character", label, name)
	}
	if !savedViewNamePattern.MatchString(name) {
		return fmt.Errorf(
			"config: %s: name %q must be letters, digits, spaces, '.', '_' or '-', starting and ending with a letter or digit",
			label, name)
	}
	return nil
}

// captureHeadingPattern matches an ATX heading with a non-empty title.
var captureHeadingPattern = regexp.MustCompile(`^#{1,6}\s+\S`)

// validateCapture runs after applyDefaults, so the inbox path is already
// normalized and Ignore already carries the merged defaults. An inbox under the
// templates dir or an ignored path would be written but never walked, so the
// capture would be invisible to every query that motivates capturing at all.
func validateCapture(cfg *Config) error {
	heading := cfg.Capture.Heading
	if containsControlRune(heading) {
		return fmt.Errorf("config: capture.heading %q contains a control character", heading)
	}
	if !captureHeadingPattern.MatchString(heading) {
		return fmt.Errorf(
			"config: capture.heading %q must be an ATX heading followed by a title (for example %q)",
			heading, DefaultCaptureHeading,
		)
	}

	inbox := cfg.Capture.InboxNote
	if containsControlRune(inbox) {
		return fmt.Errorf("config: capture.inbox_note %q contains a control character", inbox)
	}
	if pathWithinFold(inbox, cfg.Templates.Dir) {
		return fmt.Errorf(
			"config: capture.inbox_note %q must not live inside the templates directory %q",
			inbox, cfg.Templates.Dir,
		)
	}
	for _, ignored := range cfg.Ignore {
		if pathWithin(inbox, ignored) {
			return fmt.Errorf("config: capture.inbox_note %q is inside ignored path %q", inbox, ignored)
		}
	}
	return nil
}

// pathWithin reports whether the slash-separated path p is dir itself or lives
// under it. Ignore entries are compared exactly, matching Walk's map lookup.
func pathWithin(p, dir string) bool {
	if dir == "" {
		return false
	}
	return p == dir || strings.HasPrefix(p, dir+"/")
}

// pathWithinFold is pathWithin with case-insensitive, per-component comparison.
// Vault.Walk prunes the templates dir with strings.EqualFold, so an exact
// comparison here would accept a case-variant inbox that no walk ever visits.
// Comparing component by component (rather than folding the whole string) keeps
// the "/" boundaries byte-exact, so nested template dirs match only on a full
// component prefix.
func pathWithinFold(p, dir string) bool {
	if dir == "" {
		return false
	}
	want := strings.Split(dir, "/")
	got := strings.Split(p, "/")
	if len(got) < len(want) {
		return false
	}
	for i := range want {
		if !strings.EqualFold(got[i], want[i]) {
			return false
		}
	}
	return true
}

func validateAgent(cfg *Config) error {
	if cfg.Agent.TimeoutSeconds < 0 {
		return fmt.Errorf("config: agent.timeout_seconds %d must not be negative", cfg.Agent.TimeoutSeconds)
	}
	if len(cfg.Agent.Command) == 0 {
		return nil
	}
	if strings.TrimSpace(cfg.Agent.Command[0]) == "" {
		return errors.New("config: agent.command[0] must name an executable")
	}
	for i, arg := range cfg.Agent.Command {
		if strings.IndexByte(arg, 0) >= 0 {
			return fmt.Errorf("config: agent.command[%d] must not contain a NUL byte", i)
		}
	}
	return nil
}

func validateBrief(cfg *Config) error {
	for i, folder := range cfg.Brief.ExcludeFolders {
		if folder == "" {
			return fmt.Errorf("config: brief.exclude_folders[%d] must not be empty or whitespace", i)
		}
		if containsControlRune(folder) {
			return fmt.Errorf("config: brief.exclude_folders[%d] contains a control character", i)
		}
		if strings.Contains(folder, `\`) {
			return fmt.Errorf("config: brief.exclude_folders[%d] must use forward slashes", i)
		}
		if isAbsolutePathLike(folder) {
			return fmt.Errorf(
				"config: brief.exclude_folders[%d]: absolute path %q is not allowed (must be vault-relative)",
				i,
				folder,
			)
		}
		cleaned := path.Clean(folder)
		if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
			return fmt.Errorf("config: brief.exclude_folders[%d]: path %q escapes the vault", i, folder)
		}
	}

	for i, value := range cfg.Brief.ExcludeTypes {
		if value == "" {
			return fmt.Errorf("config: brief.exclude_types[%d] must not be empty or whitespace", i)
		}
		if containsControlRune(value) {
			return fmt.Errorf("config: brief.exclude_types[%d] contains a control character", i)
		}
	}
	return nil
}

// validateCockpit checks the [cockpit] section. Rule identifiers are not checked
// for membership against the cockpit's rule kinds (config stays decoupled from
// internal/cockpit), but empty or whitespace-only entries are rejected because
// they can never name a real rule and would silently do nothing.
func validateCockpit(cfg *Config) error {
	for i, rule := range cfg.Cockpit.DisabledRules {
		if strings.TrimSpace(rule) == "" {
			return fmt.Errorf("config: cockpit.disabled_rules[%d] must not be empty or whitespace", i)
		}
	}
	for i, rule := range cfg.Cockpit.Priority {
		if strings.TrimSpace(rule) == "" {
			return fmt.Errorf("config: cockpit.priority[%d] must not be empty or whitespace", i)
		}
	}
	for i, prop := range cfg.Cockpit.LongTailIgnoreProperties {
		if strings.TrimSpace(prop) == "" {
			return fmt.Errorf("config: cockpit.long_tail_ignore_properties[%d] must not be empty or whitespace", i)
		}
	}
	if cfg.Cockpit.LongTailLimit < 0 {
		return fmt.Errorf("config: cockpit.long_tail_limit %d must not be negative", cfg.Cockpit.LongTailLimit)
	}
	if cfg.Cockpit.LongTailMaxCount < 0 {
		return fmt.Errorf("config: cockpit.long_tail_max_count %d must not be negative", cfg.Cockpit.LongTailMaxCount)
	}
	if cfg.Cockpit.LongTailHeadFactor < 0 {
		return fmt.Errorf("config: cockpit.long_tail_head_factor %d must not be negative", cfg.Cockpit.LongTailHeadFactor)
	}
	for i, s := range cfg.Cockpit.ReviewStatuses {
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("config: cockpit.review_statuses[%d] must not be empty or whitespace", i)
		}
	}
	for i, s := range cfg.Cockpit.WIPStatuses {
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("config: cockpit.wip_statuses[%d] must not be empty or whitespace", i)
		}
	}
	for i, s := range cfg.Cockpit.HubTypeValues {
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("config: cockpit.hub_type_values[%d] must not be empty or whitespace", i)
		}
	}
	for i, s := range cfg.Cockpit.HubEmptyHeadings {
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("config: cockpit.hub_empty_headings[%d] must not be empty or whitespace", i)
		}
	}
	for i, s := range cfg.Cockpit.SensitiveIgnorePaths {
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("config: cockpit.sensitive_ignore_paths[%d] must not be empty or whitespace", i)
		}
	}
	for i, s := range cfg.Cockpit.SensitiveIgnorePatterns {
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("config: cockpit.sensitive_ignore_patterns[%d] must not be empty or whitespace", i)
		}
	}
	if cfg.Cockpit.DailyLimit < 0 {
		return fmt.Errorf("config: cockpit.daily_limit %d must not be negative", cfg.Cockpit.DailyLimit)
	}
	if cfg.Cockpit.MaxWIP < 0 {
		return fmt.Errorf("config: cockpit.max_wip %d must not be negative", cfg.Cockpit.MaxWIP)
	}
	if cfg.Cockpit.StaleStatusDays < 0 {
		return fmt.Errorf("config: cockpit.stale_status_days %d must not be negative", cfg.Cockpit.StaleStatusDays)
	}
	if cfg.Cockpit.MaxCaptures < 0 {
		return fmt.Errorf("config: cockpit.max_captures %d must not be negative", cfg.Cockpit.MaxCaptures)
	}
	if cfg.Cockpit.AgentTimeoutSeconds < 0 {
		return fmt.Errorf("config: cockpit.agent_timeout_seconds %d must not be negative", cfg.Cockpit.AgentTimeoutSeconds)
	}
	return nil
}

func validateTemplatesDir(dir string) error {
	for _, r := range dir {
		if r == 0 || r < 0x20 || r == 0x7f {
			return fmt.Errorf("config: templates.dir %q contains a control character (U+%04X)", dir, r)
		}
	}
	return nil
}

// validateRoutes rejects routes that escape the vault root. Values are
// vault-relative folders; an absolute path or one beginning with ".." would
// cause `bn new` to write outside the configured root.
func validateRoutes(cfg *Config) error {
	names := make([]string, 0, len(cfg.New.Routes))
	for name, dest := range cfg.New.Routes {
		names = append(names, name)
		trimmedName := strings.TrimSpace(name)
		if trimmedName == "" {
			return fmt.Errorf("config: new.routes: template name must not be empty or whitespace")
		}
		if trimmedName != name {
			return fmt.Errorf("config: new.routes[%q]: template name must not have surrounding whitespace", name)
		}
		trimmed := strings.TrimSpace(dest)
		if trimmed == "" {
			continue
		}
		slash := filepath.ToSlash(trimmed)
		if isAbsolutePathLike(slash) {
			return fmt.Errorf("config: new.routes[%q]: absolute path %q is not allowed (must be vault-relative)", name, dest)
		}
		cleaned := path.Clean(slash)
		if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
			return fmt.Errorf("config: new.routes[%q]: path %q escapes the vault", name, dest)
		}
	}
	sort.Strings(names)
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if strings.EqualFold(names[i], names[j]) {
				return fmt.Errorf("config: new.routes: template names %q and %q collide case-insensitively", names[i], names[j])
			}
		}
	}
	return nil
}

func isAbsolutePathLike(p string) bool {
	p = filepath.ToSlash(strings.TrimSpace(p))
	p = strings.ReplaceAll(p, "\\", "/")
	if strings.HasPrefix(p, "/") {
		return true
	}
	if len(p) >= 2 && isASCIIAlpha(p[0]) && p[1] == ':' {
		return len(p) == 2 || p[2] == '/'
	}
	return false
}

func isASCIIAlpha(b byte) bool {
	return ('a' <= b && b <= 'z') || ('A' <= b && b <= 'Z')
}

func validateProperties(cfg *Config) error {
	for _, name := range cfg.Frontmatter.PropertyNames() {
		def := cfg.Frontmatter.Properties[name]
		trimmedName := strings.TrimSpace(name)
		if trimmedName == "" {
			return fmt.Errorf("config: frontmatter.properties: property name must not be empty or whitespace")
		}
		if trimmedName != name {
			return fmt.Errorf("config: frontmatter.properties: property %q: name must not have surrounding whitespace", name)
		}
		if def.Type == "" {
			return fmt.Errorf("config: frontmatter.properties: property %q: type is required", name)
		}
		if !ValidPropertyKind(def.Type) {
			return fmt.Errorf("config: frontmatter.properties: property %q: unknown type %q", name, def.Type)
		}
		if def.IsList() {
			if def.Items == "" {
				return fmt.Errorf("config: frontmatter.properties: property %q: list type requires items", name)
			}
			if !ValidScalarKind(def.Items) {
				return fmt.Errorf("config: frontmatter.properties: property %q: unknown items kind %q", name, def.Items)
			}
		} else if def.Items != "" {
			return fmt.Errorf("config: frontmatter.properties: property %q: items is only valid when type = \"list\"", name)
		}
		// Reject empty / whitespace-only entries inside `allowed` — they
		// would silently let an empty (or pure-whitespace) value pass the
		// allowed-values check at lint time, defeating the intent of the
		// constraint.
		for i, v := range def.Allowed {
			if strings.TrimSpace(v) == "" {
				return fmt.Errorf("config: frontmatter.properties: property %q: allowed[%d] must not be empty or whitespace", name, i)
			}
		}
	}
	return nil
}
