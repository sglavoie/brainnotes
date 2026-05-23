package config

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func validate(cfg *Config) error {
	if cfg.Vault == "" {
		return errors.New("config: vault path is not set (use --vault, BNOTES_VAULT, or 'vault' in config.toml)")
	}
	info, err := os.Stat(cfg.Vault)
	if err != nil {
		return fmt.Errorf("config: vault path %q: %w", cfg.Vault, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("config: vault path %q is not a directory", cfg.Vault)
	}
	if err := validateSchema(cfg); err != nil {
		return err
	}
	if err := validateRoutes(cfg); err != nil {
		return err
	}
	return nil
}

// validateRoutes rejects routes that escape the vault root. Values are
// vault-relative folders; an absolute path or one beginning with ".." would
// cause `bn new` to write outside the configured root.
func validateRoutes(cfg *Config) error {
	for name, dest := range cfg.New.Routes {
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

var validSchemaTypes = map[string]bool{
	"string": true,
	"list":   true,
	"date":   true,
	"int":    true,
	"bool":   true,
	"float":  true,
}

func validateSchema(cfg *Config) error {
	for name, rule := range cfg.Frontmatter.Schema {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("config: schema: property name must not be empty or whitespace")
		}
		if rule.Type == "" {
			return fmt.Errorf("config: schema: property %q: type is required", name)
		}
		if !validSchemaTypes[rule.Type] {
			return fmt.Errorf("config: schema: property %q: unknown type %q", name, rule.Type)
		}
		if len(rule.Allowed) > 0 && rule.Type != "string" && rule.Type != "list" {
			return fmt.Errorf("config: schema: property %q: allowed is only valid for string or list types, got %q", name, rule.Type)
		}
	}
	return nil
}
