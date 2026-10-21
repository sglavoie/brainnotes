package web

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"sync"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/noteops"
	"github.com/sglavoie/brainnotes/internal/vault"
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
	// SourceHash is the SHA-256 of the published brainnotes.toml bytes. It names
	// one layer of two and is deliberately not the answer to "did the
	// configuration change" — CompositeHash is. It stays the portable file's own
	// digest because it is how a caller identifies *which vault* a server loaded.
	SourceHash string `json:"source_hash"`
	// LocalSourceHash is the SHA-256 of the published brainnotes.local.toml
	// bytes, and empty when the vault carries no overlay.
	LocalSourceHash string `json:"local_source_hash,omitempty"`
	// LocalPresent reports whether the overlay existed when this configuration
	// was published. An empty overlay hashes to the digest of no bytes rather
	// than to the empty string, so this is what separates "no overlay" from "an
	// empty one" — and creating one is a change even before anything is typed
	// into it.
	LocalPresent bool `json:"local_present"`
	// CompositeHash covers both layers and the presence of the overlay. It is the
	// value to compare when the question is whether the configuration changed.
	CompositeHash string `json:"composite_hash"`
	// Status is ok while the configuration on disk is valid, invalid otherwise.
	Status ReloadStatus `json:"status"`
	// Error is the validation failure that kept the last valid configuration
	// published. Empty when Status is ok.
	Error string `json:"error,omitempty"`
	// RestartRequired lists startup-only keys that differ between the published
	// configuration and the one the server booted with.
	RestartRequired []string `json:"restart_required,omitempty"`
	// Writable reports whether property mutations are currently allowed.
	Writable bool `json:"writable"`
	// Mode names the serving profile: local, read-only, or hosted. It is filled
	// by the handler rather than by the configuration manager, because it is a
	// property of the deployment and not of any file on disk.
	Mode string `json:"mode,omitempty"`
	// ProcessLaunch reports whether this deployment may start the configured
	// editor or agent command. It is the capability behind every Open-in-editor
	// and Ask-AI control, published so a client can tell "not configured" from
	// "not available here".
	ProcessLaunch bool `json:"process_launch"`
}

// configLayerPin is one configuration file as a mutation found it.
//
// Absence is a pinned state like any other: an overlay that did not exist when
// the write was planned is required to still not exist when it commits, because
// a file created in between can override any key the plan relied on.
type configLayerPin struct {
	Path string
	// Hash is the SHA-256 of the file's bytes, and empty when Present is false.
	Hash    string
	Present bool
}

// expectation is the transaction precondition for this layer.
func (p configLayerPin) expectation() vault.FileExpectation {
	if !p.Present {
		return vault.ExpectMissing()
	}
	return vault.ExpectHash(p.Hash)
}

// name is the file's base name, for a message about it.
func (p configLayerPin) name() string { return filepath.Base(p.Path) }

// configPin names the exact configuration a mutation was validated against —
// both layers, because brainnotes.local.toml overrides whatever it assigns and
// can be created, edited, or deleted with brainnotes.toml untouched. Every
// config-sensitive write carries the pin into its transaction, which verifies
// both files again at the commit boundary, so a note can never be written under
// declarations that were replaced after the write was planned.
type configPin struct {
	Portable   configLayerPin
	Local      configLayerPin
	Generation int64
}

// tracked reports whether the pin names files the server reloads from. A server
// built without a readable brainnotes.toml has nothing to pin: that file is the
// vault-root marker, so without it there is no vault to locate an overlay in
// either.
func (p configPin) tracked() bool { return p.Portable.Path != "" && p.Portable.Hash != "" }

// layers lists every configuration file the pin requires, in a fixed order.
func (p configPin) layers() []configLayerPin {
	if !p.tracked() {
		return nil
	}
	layers := []configLayerPin{p.Portable}
	if p.Local.Path != "" {
		layers = append(layers, p.Local)
	}
	return layers
}

// frontmatterConfigPinOptions carries the pin into a single-note write.
func frontmatterConfigPinOptions(pin configPin) []noteops.FrontmatterOption {
	layers := pin.layers()
	if len(layers) == 0 {
		return nil
	}
	opts := make([]noteops.FrontmatterOption, 0, len(layers))
	for _, layer := range layers {
		opts = append(opts, noteops.WithFrontmatterRequire(layer.Path, layer.expectation()))
	}
	return opts
}

// bulkConfigPinOptions carries the pin into every note write of a bulk action.
func bulkConfigPinOptions(pin configPin) []noteops.BulkOption {
	layers := pin.layers()
	if len(layers) == 0 {
		return nil
	}
	opts := make([]noteops.BulkOption, 0, len(layers))
	for _, layer := range layers {
		opts = append(opts, noteops.WithBulkRequire(layer.Path, layer.expectation()))
	}
	return opts
}

// sourceConfigPinOptions carries the pin into a raw-Markdown save. A source save
// never re-renders frontmatter, but whether the properties block it submitted is
// acceptable is a question about the vault's declarations, and answering it
// against a configuration that has since been replaced is answering the wrong
// question.
func sourceConfigPinOptions(pin configPin) []noteops.SourceOption {
	layers := pin.layers()
	if len(layers) == 0 {
		return nil
	}
	opts := make([]noteops.SourceOption, 0, len(layers))
	for _, layer := range layers {
		opts = append(opts, noteops.WithSourceRequire(layer.Path, layer.expectation()))
	}
	return opts
}

// configManager owns every read of the vault configuration for the server. It
// publishes immutable deep clones, so a reload can never mutate a snapshot a
// request is already using, and it tracks the reload bookkeeping the status
// endpoint exposes.
type configManager struct {
	vault *vault.Vault
	// root is the vault both configuration layers are read from. It is fixed at
	// construction, so every reload reads the same pair of paths.
	root string
	// sourcePath and localSourcePath are the two layers. The overlay has a path
	// whether or not it exists, which is what lets a pin require its absence.
	sourcePath      string
	localSourcePath string
	// watching is false when no readable brainnotes.toml existed at boot, which
	// makes Refresh a no-op: there is no file to reload from, so the boot
	// configuration stays published instead of being reported as invalid.
	watching bool
	// bootSerial fingerprints the startup-only fields at boot so a later reload
	// can report which of them now need a restart.
	bootSerial map[string]string

	mu        sync.RWMutex
	published *config.Config
	// generation increments once per published configuration, and fingerprint is
	// the layer digests it was published from. They move together under the one
	// lock, so a caller that reads both is never pairing one generation's
	// declarations with another generation's hashes.
	fingerprint     config.Fingerprint
	generation      int64
	status          ReloadStatus
	validationError string
	restartRequired []string
	// redactor takes the vault root out of the reported validation error in a
	// mode whose readers are remote. The stored error keeps the full text.
	redactor vaultRootRedactor
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
		root:       cfg.Vault,
		sourcePath: cfg.SourcePath,
		bootSerial: startupOnlySerial(published),
		published:  published,
		generation: 1,
		status:     ReloadOK,
	}
	if m.root != "" {
		m.localSourcePath = config.LayerPath(m.root, config.LayerLocal)
	}
	if sources, err := config.ReadSources(m.root); err == nil {
		m.fingerprint = sources.Fingerprint()
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
		SourceHash:      m.fingerprint.Portable,
		LocalSourceHash: m.fingerprint.Local,
		LocalPresent:    m.fingerprint.LocalPresent,
		CompositeHash:   m.fingerprint.Composite,
		Status:          m.status,
		Error:           m.redactor.redact(m.validationError),
		RestartRequired: append([]string(nil), m.restartRequired...),
		Writable:        m.status == ReloadOK,
	}
}

// Refresh re-reads both configuration layers and republishes them when the pair
// changed. It reports whether a new valid configuration was published, so
// callers can invalidate dependent caches exactly once per change. An invalid
// configuration leaves the last valid one published and returns nil: the failure
// is a visible status, not a request error.
//
// The comparison is on the composite fingerprint rather than on either file's
// own digest, because either file alone changes what the vault does: editing the
// overlay, creating an empty one, and deleting it again are three distinct
// configurations with identical brainnotes.toml bytes.
func (m *configManager) Refresh() (bool, error) {
	if !m.watching {
		return false, nil
	}
	sources, err := config.ReadSources(m.root)
	if err != nil {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.status = ReloadInvalid
		m.validationError = err.Error()
		return false, nil
	}
	fingerprint := sources.Fingerprint()

	m.mu.RLock()
	unchanged := m.publishedLocked(fingerprint)
	origin := m.published.VaultOrigin
	m.mu.RUnlock()
	if unchanged {
		return false, nil
	}

	loaded, decodeErr := config.DecodeSources(m.root, origin, sources)
	m.mu.Lock()
	defer m.mu.Unlock()
	if decodeErr != nil {
		m.status = ReloadInvalid
		m.validationError = decodeErr.Error()
		return false, nil
	}
	if m.publishedLocked(fingerprint) {
		return false, nil
	}

	published := loaded.Clone()
	m.published = published
	m.fingerprint = fingerprint
	m.generation++
	m.status = ReloadOK
	m.validationError = ""
	m.restartRequired = restartRequiredFields(m.bootSerial, published)
	m.vault.SetConfig(published)
	return true, nil
}

// publishedLocked reports whether these exact layer bytes are already the
// published configuration. A ReloadInvalid status is never "already published"
// even on a matching fingerprint: the manager may have been invalidated by
// something other than the bytes, such as the vault becoming enrolled.
func (m *configManager) publishedLocked(fingerprint config.Fingerprint) bool {
	return fingerprint.Composite == m.fingerprint.Composite && m.status == ReloadOK
}

// pin names the files the given status was published from. status must come from
// the same read as the configuration the caller planned with.
func (m *configManager) pin(status ConfigStatus) configPin {
	return configPin{
		Portable:   configLayerPin{Path: m.sourcePath, Hash: status.SourceHash, Present: true},
		Local:      configLayerPin{Path: m.localSourcePath, Hash: status.LocalSourceHash, Present: status.LocalPresent},
		Generation: status.Generation,
	}
}

// planConfigSources reads both configuration layers for a planner about to patch
// one of them, and pins the overlay it read.
//
// It reads from disk rather than taking the published bytes because a plan's
// preconditions have to name what is on disk now. Both layers come back together
// because patched brainnotes.toml bytes only have a meaning in company with the
// overlay that overrides them.
func planConfigSources(root string) (config.Sources, configLayerPin, error) {
	sources, err := config.ReadSources(root)
	if err != nil {
		// Either layer can be the unreadable one, and the wrapped error names the
		// path, so naming a file here would be a guess that can contradict it.
		return config.Sources{}, configLayerPin{}, noteops.WrapError(
			noteops.CodeInternal, "config: read the vault configuration", err)
	}
	pin := configLayerPin{
		Path:    config.LayerPath(root, config.LayerLocal),
		Present: sources.LocalPresent(),
	}
	if pin.Present {
		pin.Hash = vault.HashBytes(sources.Local)
	}
	return sources, pin, nil
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

// reloadConfig re-reads both configuration layers and drops the cached vault
// snapshot when a new valid configuration was published, because note
// diagnostics, schema checks, and the cockpit queue are all derived from it.
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

// finishConfigWrite republishes the configuration after a write that touched
// brainnotes.toml alone. Unlike finishPropertyWrite it does not invalidate the
// vault snapshot itself: no note changed, so nothing derived from the notes is
// stale. reloadConfig still drops the snapshot when the file really changed,
// because it bumps the generation — and [views] is deliberately not excluded
// from that generation. A view save therefore costs one properties.Build and one
// recall corpus rebuild, which is the accepted price of a generation that means
// "the configuration changed" rather than "some parts of it did"; the narrower
// meaning would be a footgun for every pin that compares it.
func (s *Server) finishConfigWrite(changed bool) {
	if !changed {
		return
	}
	_ = s.reloadConfig()
}

// ensureConfigWritable reloads before a property mutation so the write uses the
// configuration currently on disk, and refuses the write outright when that
// configuration is invalid — property semantics come from it, so applying a
// mutation against a stale definition could silently write the wrong shape. It
// only opens the window; the configPin the mutation carries is what keeps a
// later edit from landing inside it.
//
// Either layer can be the invalid one, so the message names the configuration
// rather than a file. The wrapped error already names the file that failed.
func (s *Server) ensureConfigWritable() error {
	if err := s.reloadConfig(); err != nil {
		return noteops.WrapError(noteops.CodeInternal, "config: reload failed", err)
	}
	status := s.configState.Status()
	if status.Status == ReloadOK {
		return nil
	}
	return noteops.Errorf(noteops.CodeConflict,
		"config: this vault's configuration is invalid, so property writes are disabled until it is fixed: %s",
		status.Error)
}
