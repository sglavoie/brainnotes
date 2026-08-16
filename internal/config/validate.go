package config

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
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
	if err := validateCockpit(cfg); err != nil {
		return err
	}
	return nil
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
