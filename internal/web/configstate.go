package web

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// ReloadStatus reports whether the configuration currently on disk was
// accepted.
type ReloadStatus string

const (
	// ReloadOK means the published configuration matches the file on disk.
	ReloadOK ReloadStatus = "ok"
	// ReloadInvalid means the file on disk failed to parse or validate. The
	// last valid configuration stays published and property writes are refused.
	ReloadInvalid ReloadStatus = "invalid"
)

// startupOnlyFields names the configuration keys that only take effect when the
// server binds its listener, so a change to them needs a restart rather than a
// reload.
var startupOnlyFields = []string{"serve.addr", "serve.open_browser"}

// ConfigStatus is the immutable view of the manager's reload bookkeeping.
type ConfigStatus struct {
	// Generation increments every time a new valid configuration is published.
	Generation int64 `json:"generation"`
	// SourceHash is the SHA-256 of the published configuration's file bytes.
	SourceHash string `json:"source_hash"`
	// Status is ok while the on-disk file is valid, invalid otherwise.
	Status ReloadStatus `json:"status"`
	// Error is the validation failure that kept the last valid configuration
	// published. Empty when Status is ok.
	Error string `json:"error,omitempty"`
	// RestartRequired lists startup-only keys that differ between the published
	// configuration and the one the server booted with.
	RestartRequired []string `json:"restart_required,omitempty"`
	// Writable reports whether property mutations are currently allowed.
	Writable bool `json:"writable"`
}

// configPin names the exact brainnotes.toml a mutation was validated against.
// Every config-sensitive write carries it into its transaction, which verifies
// the file again at the commit boundary, so a note can never be written under
// declarations that were replaced after the write was planned.
type configPin struct {
	Path       string
	Hash       string
	Generation int64
}

// tracked reports whether the pin names a file the server reloads from. A
// server built without a brainnotes.toml on disk has nothing to pin.
func (p configPin) tracked() bool { return p.Path != "" && p.Hash != "" }

// expectation is the transaction precondition for the pinned file.
func (p configPin) expectation() vault.FileExpectation { return vault.ExpectHash(p.Hash) }

// frontmatterConfigPinOptions carries the pin into a single-note write.
func frontmatterConfigPinOptions(pin configPin) []noteops.FrontmatterOption {
	if !pin.tracked() {
		return nil
	}
	return []noteops.FrontmatterOption{noteops.WithFrontmatterRequire(pin.Path, pin.expectation())}
}

// bulkConfigPinOptions carries the pin into every note write of a bulk action.
func bulkConfigPinOptions(pin configPin) []noteops.BulkOption {
	if !pin.tracked() {
		return nil
	}
	return []noteops.BulkOption{noteops.WithBulkRequire(pin.Path, pin.expectation())}
}

// configManager owns every read of the vault configuration for the server. It
// publishes immutable deep clones, so a reload can never mutate a snapshot a
// request is already using, and it tracks the reload bookkeeping the status
// endpoint exposes.
type configManager struct {
	vault      *vault.Vault
	sourcePath string
	// watching is false when no readable brainnotes.toml existed at boot, which
	// makes Refresh a no-op: there is no file to reload from, so the boot
	// configuration stays published instead of being reported as invalid.
	watching bool
	// bootSerial fingerprints the startup-only fields at boot so a later reload
	// can report which of them now need a restart.
	bootSerial map[string]string

	mu              sync.RWMutex
	published       *config.Config
	generation      int64
	sourceHash      string
	status          ReloadStatus
	validationError string
	restartRequired []string
}

func newConfigManager(cfg *config.Config, v *vault.Vault) (*configManager, error) {
	if cfg == nil {
		return nil, errors.New("web: config is required")
	}
	if v == nil {
		return nil, errors.New("web: vault is required")
	}
	published := cfg.Clone()
	m := &configManager{
		vault:      v,
		sourcePath: cfg.SourcePath,
		bootSerial: startupOnlySerial(published),
		published:  published,
		generation: 1,
		status:     ReloadOK,
	}
	if hash, err := m.readSourceHash(); err == nil {
		m.sourceHash = hash
		m.watching = true
	}
	v.SetConfig(published)
	return m, nil
}

// Snapshot returns the published configuration. The returned pointer is never
// mutated after publication, so callers may read it without further locking;
// they must not write to it.
func (m *configManager) Snapshot() *config.Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.published
}

// Status reports the current reload bookkeeping.
func (m *configManager) Status() ConfigStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.statusLocked()
}

// publication returns the published configuration together with the reload
// bookkeeping it was published from. Reading both under one lock is what lets a
// caller pin a mutation to the exact file its definitions came from, instead of
// pairing one generation's declarations with another generation's hash.
func (m *configManager) publication() (*config.Config, ConfigStatus) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.published, m.statusLocked()
}

func (m *configManager) statusLocked() ConfigStatus {
	return ConfigStatus{
		Generation:      m.generation,
		SourceHash:      m.sourceHash,
		Status:          m.status,
		Error:           m.validationError,
		RestartRequired: append([]string(nil), m.restartRequired...),
		Writable:        m.status == ReloadOK,
	}
}

// Refresh re-reads brainnotes.toml and republishes it when its bytes changed.
// It reports whether a new valid configuration was published, so callers can
// invalidate dependent caches exactly once per change. An invalid file leaves
// the last valid configuration published and returns nil: the failure is a
// visible status, not a request error.
func (m *configManager) Refresh() (bool, error) {
	if !m.watching {
		return false, nil
	}
	source, hash, err := m.readSource()
	if err != nil {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.status = ReloadInvalid
		m.validationError = err.Error()
		return false, nil
	}

	m.mu.RLock()
	unchanged := hash == m.sourceHash && m.status == ReloadOK
	root := m.published.Vault
	origin := m.published.VaultOrigin
	m.mu.RUnlock()
	if unchanged {
		return false, nil
	}

	loaded, decodeErr := config.Decode(root, origin, source)
	m.mu.Lock()
	defer m.mu.Unlock()
	if decodeErr != nil {
		m.status = ReloadInvalid
		m.validationError = decodeErr.Error()
		return false, nil
	}
	if hash == m.sourceHash && m.status == ReloadOK {
		return false, nil
	}

	published := loaded.Clone()
	m.published = published
	m.sourceHash = hash
	m.generation++
	m.status = ReloadOK
	m.validationError = ""
	m.restartRequired = restartRequiredFields(m.bootSerial, published)
	m.vault.SetConfig(published)
	return true, nil
}

// pin names the file the given status was published from. status must come from
// the same read as the configuration the caller planned with.
func (m *configManager) pin(status ConfigStatus) configPin {
	return configPin{Path: m.sourcePath, Hash: status.SourceHash, Generation: status.Generation}
}

func (m *configManager) readSource() ([]byte, string, error) {
	source, err := os.ReadFile(m.sourcePath)
	if err != nil {
		return nil, "", fmt.Errorf("web: read %s: %w", m.sourcePath, err)
	}
	return source, vault.HashBytes(source), nil
}

func (m *configManager) readSourceHash() (string, error) {
	_, hash, err := m.readSource()
	return hash, err
}

// startupOnlySerial renders the startup-only fields as comparable strings.
func startupOnlySerial(cfg *config.Config) map[string]string {
	return map[string]string{
		"serve.addr":         cfg.Serve.Addr,
		"serve.open_browser": fmt.Sprintf("%t", cfg.Serve.BrowserEnabled()),
	}
}

func restartRequiredFields(boot map[string]string, cfg *config.Config) []string {
	current := startupOnlySerial(cfg)
	var changed []string
	for _, field := range startupOnlyFields {
		if boot[field] != current[field] {
			changed = append(changed, field)
		}
	}
	sort.Strings(changed)
	return changed
}

// config returns the server's published configuration snapshot.
func (s *Server) config() *config.Config {
	return s.configState.Snapshot()
}

// pinnedConfig returns the published configuration, its reload bookkeeping, and
// the pin naming the file it was decoded from. A mutation plans with the
// returned configuration and hands the pin to its write, so the bytes that
// decided the change are the bytes verified at the commit boundary.
func (s *Server) pinnedConfig() (*config.Config, ConfigStatus, configPin) {
	cfg, status := s.configState.publication()
	return cfg, status, s.configState.pin(status)
}

// reloadConfig re-reads brainnotes.toml and drops the cached vault snapshot
// when a new valid configuration was published, because note diagnostics,
// schema checks, and the cockpit queue are all derived from it.
func (s *Server) reloadConfig() error {
	changed, err := s.configState.Refresh()
	if err != nil {
		return err
	}
	if changed {
		s.state.invalidate()
	}
	return nil
}

// ensureConfigWritable reloads before a property mutation so the write uses the
// configuration currently on disk, and refuses the write outright when that
// file is invalid — property semantics come from it, so applying a mutation
// against a stale definition could silently write the wrong shape. It only
// opens the window; the configPin the mutation carries is what keeps a later
// edit from landing inside it.
func (s *Server) ensureConfigWritable() error {
	if err := s.reloadConfig(); err != nil {
		return noteops.WrapError(noteops.CodeInternal, "config: reload failed", err)
	}
	status := s.configState.Status()
	if status.Status == ReloadOK {
		return nil
	}
	return noteops.Errorf(noteops.CodeConflict,
		"config: %s is invalid, so property writes are disabled until it is fixed: %s",
		config.FileName, status.Error)
}
