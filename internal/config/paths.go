package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// expandUser expands a leading "~" or "~/" in p to the user's home directory.
// A bare "~" expands to the home directory; "~/foo" expands to "<home>/foo".
// "~user" style paths are not supported and are returned unchanged.
// An empty path is returned unchanged.
func expandUser(p string) (string, error) {
	if p == "" {
		return p, nil
	}
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~"+string(os.PathSeparator)) {
		home, err := os.UserHomeDir()
		if err != nil {
			return p, err
		}
		if p == "~" {
			return home, nil
		}
		return filepath.Join(home, p[2:]), nil
	}
	return p, nil
}

// ConfigSource identifies which mechanism contributed a candidate path.
type ConfigSource int

const (
	// SourceFlag is the --config CLI flag.
	SourceFlag ConfigSource = iota
	// SourceEnv is $BNOTES_CONFIG.
	SourceEnv
	// SourceXDG is $XDG_CONFIG_HOME/bnotes/config.toml.
	SourceXDG
	// SourceDefault is ~/.config/bnotes/config.toml.
	SourceDefault
)

// CandidatePath is one entry in the resolution chain.
type CandidatePath struct {
	Source ConfigSource
	Label  string // human-readable source label
	Path   string // empty when the source is not set
	Exists bool   // true only if Path != "" and the file exists
	Active bool   // true on the first entry with a non-empty Path
}

// Candidates returns the full precedence chain (4 entries, in source order).
// An error is only returned when the active (highest-precedence) candidate
// cannot be resolved (e.g. tilde expansion failed for the source that would
// be used, or os.UserHomeDir fails and the default-path source would be used).
// Lower-precedence sources that fail to expand are reported with an empty
// Path so a higher-precedence source can still produce a usable result.
//
// Leading "~"/"~/" in the --config flag, $BNOTES_CONFIG, and $XDG_CONFIG_HOME
// values is expanded to the user's home directory.
//
// Whitespace-only values for the flag, $BNOTES_CONFIG, or $XDG_CONFIG_HOME
// are treated as unset so they fall through to the next precedence level
// rather than producing confusing "file does not exist" errors.
func Candidates(flagConfigPath string) ([]CandidatePath, error) {
	// Track expansion errors per source so we can surface them only if the
	// failing source would have been the active (highest-priority) candidate.
	flagTrimmed := strings.TrimSpace(flagConfigPath)
	flagPath, flagErr := expandUser(flagTrimmed)
	if flagErr != nil {
		// Flag is highest priority; if it was provided and fails to expand,
		// surface the error immediately.
		return nil, fmt.Errorf("config: expand --config %q: %w", flagConfigPath, flagErr)
	}

	envRaw := strings.TrimSpace(os.Getenv(envConfig))
	envPath, envErr := expandUser(envRaw)
	if envErr != nil {
		// If the flag is set, it wins anyway — degrade gracefully so a bad
		// env value cannot break a working --config invocation.
		if flagPath != "" {
			envPath = ""
		} else {
			return nil, fmt.Errorf("config: expand $BNOTES_CONFIG: %w", envErr)
		}
	}

	out := []CandidatePath{
		{Source: SourceFlag, Label: "--config flag", Path: flagPath},
		{Source: SourceEnv, Label: "$BNOTES_CONFIG", Path: envPath},
		{Source: SourceXDG, Label: "$XDG_CONFIG_HOME"},
		{Source: SourceDefault, Label: "default path"},
	}

	if xdg := strings.TrimSpace(os.Getenv(envXDG)); xdg != "" {
		expanded, xdgErr := expandUser(xdg)
		if xdgErr != nil {
			// XDG is third in precedence; if flag or env is set, fall through.
			if flagPath == "" && envPath == "" {
				return nil, fmt.Errorf("config: expand $XDG_CONFIG_HOME: %w", xdgErr)
			}
		} else {
			out[2].Path = filepath.Join(expanded, configName)
		}
	}

	anySet := out[0].Path != "" || out[1].Path != "" || out[2].Path != ""
	home, homeErr := os.UserHomeDir()
	if homeErr != nil {
		if !anySet {
			return nil, fmt.Errorf("config: resolve home dir: %w", homeErr)
		}
	} else {
		out[3].Path = filepath.Join(home, ".config", configName)
	}

	activeSet := false
	for i := range out {
		if out[i].Path == "" {
			continue
		}
		if !activeSet {
			out[i].Active = true
			activeSet = true
		}
		if info, err := os.Stat(out[i].Path); err == nil && !info.IsDir() {
			out[i].Exists = true
		}
	}

	return out, nil
}

// ResolvePath returns the active config path Load would consult. ok is false
// when no candidate produced a non-empty path. The returned path may not exist.
func ResolvePath(flagConfigPath string) (path string, ok bool, err error) {
	cands, err := Candidates(flagConfigPath)
	if err != nil {
		return "", false, err
	}
	for _, c := range cands {
		if c.Active {
			return c.Path, true, nil
		}
	}
	return "", false, nil
}

// resolveConfigPath returns the chosen config path and whether it was set explicitly
// (via flag or BNOTES_CONFIG). For explicit sources, a missing file is an error;
// for the default search paths, a missing file is fine.
func resolveConfigPath(flagConfigPath string) (path string, explicit bool, err error) {
	cands, err := Candidates(flagConfigPath)
	if err != nil {
		return "", false, err
	}
	for _, c := range cands {
		if !c.Active {
			continue
		}
		return c.Path, c.Source == SourceFlag || c.Source == SourceEnv, nil
	}
	return "", false, nil
}
