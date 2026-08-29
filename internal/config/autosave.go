package config

import (
	"fmt"
	"strings"
	"time"
)

// AutosaveOff is the only non-duration value autosave.min_interval accepts. It
// stops the external watcher creating its own commits; scanning, deletion
// protection, health reporting, and explicit mutations continue unchanged.
const AutosaveOff = "off"

// DefaultAutosaveMinInterval is the floor that applies when
// [autosave].min_interval is absent.
const DefaultAutosaveMinInterval = "5m"

// MinAutosaveInterval and MaxAutosaveInterval bound a configured duration,
// inclusively. The lower bound keeps the floor above the watcher's own 30s
// quiet debounce; the upper one keeps a typo from silently parking a vault's
// autosaves for months.
const (
	MinAutosaveInterval = 30 * time.Second
	MaxAutosaveInterval = 24 * time.Hour
)

// AutosavePolicy is one resolved autosave.min_interval value: the single place
// config loading, CLI output, and the watcher agree on what a stored string
// means. Disabled and Interval are mutually exclusive — a disabled policy has a
// zero Interval — and Canonical is the normalized spelling both `bn config get`
// and the watcher's persisted state write.
type AutosavePolicy struct {
	// Disabled reports the `off` policy: no external autosave commits.
	Disabled bool
	// Interval is the floor between two external autosave commits. Zero when
	// Disabled.
	Interval time.Duration
	// Canonical is AutosaveOff or the normalized duration spelling ("5m", not
	// "300s" or "5m0s").
	Canonical string
}

// String returns the canonical spelling, so a policy formats identically
// wherever it is printed.
func (p AutosavePolicy) String() string { return p.Canonical }

// ParseAutosaveMinInterval resolves one stored autosave.min_interval value.
//
// Empty or whitespace-only input means "unset" and resolves to
// DefaultAutosaveMinInterval, so a caller holding raw bytes and a caller
// holding a loaded Config reach the same answer. Every other spelling must be
// exactly AutosaveOff or a Go duration inside the inclusive bounds; `Off` and
// `5M` are rejected rather than guessed at, because a config value that means
// "never commit" is not a place to be lenient.
func ParseAutosaveMinInterval(raw string) (AutosavePolicy, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		value = DefaultAutosaveMinInterval
	}
	if value == AutosaveOff {
		return AutosavePolicy{Disabled: true, Canonical: AutosaveOff}, nil
	}
	if strings.EqualFold(value, AutosaveOff) {
		return AutosavePolicy{}, fmt.Errorf(
			"config: autosave.min_interval %q must be spelled %q in lower case", raw, AutosaveOff)
	}
	interval, err := time.ParseDuration(value)
	if err != nil {
		return AutosavePolicy{}, fmt.Errorf(
			"config: autosave.min_interval %q must be %q or a Go duration such as %q",
			raw, AutosaveOff, DefaultAutosaveMinInterval)
	}
	if interval < MinAutosaveInterval || interval > MaxAutosaveInterval {
		return AutosavePolicy{}, fmt.Errorf(
			"config: autosave.min_interval %q must be between %s and %s inclusive",
			raw, CanonicalAutosaveDuration(MinAutosaveInterval), CanonicalAutosaveDuration(MaxAutosaveInterval))
	}
	return AutosavePolicy{Interval: interval, Canonical: CanonicalAutosaveDuration(interval)}, nil
}

// AutosavePolicy resolves the vault's external autosave floor. Load and Decode
// validate and canonicalize the stored value, so a config that came from either
// always resolves; a hand-built Config carrying a value neither would have
// accepted falls back to DefaultAutosaveMinInterval rather than reporting an
// error every caller would have to handle identically. Callers holding raw text
// (a patch, a CLI argument) validate it with ParseAutosaveMinInterval instead.
func (c *Config) AutosavePolicy() AutosavePolicy {
	raw := DefaultAutosaveMinInterval
	if c != nil && strings.TrimSpace(c.Autosave.MinInterval) != "" {
		raw = c.Autosave.MinInterval
	}
	policy, err := ParseAutosaveMinInterval(raw)
	if err != nil {
		policy, _ = ParseAutosaveMinInterval(DefaultAutosaveMinInterval)
	}
	return policy
}

// CanonicalAutosaveDuration is the one duration spelling this project writes:
// time.Duration.String with its zero-valued trailing units dropped, so 300s
// renders as "5m" and 24h as "24h" rather than "5m0s" and "24h0m0s".
func CanonicalAutosaveDuration(d time.Duration) string {
	text := d.String()
	if strings.HasSuffix(text, "m0s") {
		text = strings.TrimSuffix(text, "0s")
	}
	if strings.HasSuffix(text, "h0m") {
		text = strings.TrimSuffix(text, "0m")
	}
	return text
}
