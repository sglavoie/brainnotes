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
	Active bool   // true on the path Load/ResolvePath would consult
}

type candidatePathState uint8

const (
	candidatePathMissing candidatePathState = iota
	candidatePathFile
	candidatePathDirectory
)

func inspectCandidatePath(path string) (candidatePathState, error) {
	info, err := os.Stat(path)
	if err == nil {
		if info.IsDir() {
			return candidatePathDirectory, nil
		}
		return candidatePathFile, nil
	}
	if os.IsNotExist(err) {
		if ancestorErr := inspectCandidateAncestorPath(path, err); ancestorErr != nil {
			return candidatePathMissing, ancestorErr
		}
		return candidatePathMissing, nil
	}
	return candidatePathMissing, fmt.Errorf("config: stat %s: %w", path, err)
}

func inspectCandidateAncestorPath(path string, statErr error) error {
	for current := filepath.Clean(path); ; {
		info, err := os.Lstat(current)
		switch {
		case err == nil:
			if info.Mode()&os.ModeSymlink != 0 {
				if _, serr := os.Stat(current); serr != nil {
					if !os.IsNotExist(serr) {
						return fmt.Errorf("config: stat %s: %w", current, serr)
					}
					return fmt.Errorf("config: stat %s: %w", path, statErr)
				}
			}
		case os.IsNotExist(err):
		default:
			return fmt.Errorf("config: lstat %s: %w", current, err)
		}

		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
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
//
// If the active candidate names a directory instead of a TOML file, or if
// statting that candidate fails for a reason other than a genuinely missing
// file, Candidates returns an error rather than silently falling through to a
// lower-precedence source or handing the bad path to callers such as
// `bn config edit`.
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
	states := make([]candidatePathState, len(out))
	statErrs := make([]error, len(out))

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

	for i := range out {
		if out[i].Path == "" {
			continue
		}
		state, err := inspectCandidatePath(out[i].Path)
		states[i] = state
		statErrs[i] = err
		if state == candidatePathFile {
			out[i].Exists = true
		}
	}

	switch {
	case out[0].Path != "":
		if statErrs[0] != nil {
			return nil, statErrs[0]
		}
		if states[0] == candidatePathDirectory {
			return nil, fmt.Errorf("config: %s is a directory, expected a TOML file", out[0].Path)
		}
		out[0].Active = true
	case out[1].Path != "":
		if statErrs[1] != nil {
			return nil, statErrs[1]
		}
		if states[1] == candidatePathDirectory {
			return nil, fmt.Errorf("config: %s is a directory, expected a TOML file", out[1].Path)
		}
		out[1].Active = true
	default:
		firstImplicit := -1
		for i := 2; i < len(out); i++ {
			if out[i].Path == "" {
				continue
			}
			if firstImplicit == -1 {
				firstImplicit = i
			}
			if statErrs[i] != nil {
				return nil, statErrs[i]
			}
			if states[i] == candidatePathDirectory {
				return nil, fmt.Errorf("config: %s is a directory, expected a TOML file", out[i].Path)
			}
			if out[i].Exists {
				out[i].Active = true
				return out, nil
			}
		}
		if firstImplicit != -1 {
			out[firstImplicit].Active = true
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
