package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// LocalFileName is the optional machine-local overlay that sits beside
// FileName at the vault root. It is never the vault-root marker: a directory is
// a vault because it holds brainnotes.toml, and a vault with no overlay is the
// normal, legacy shape rather than a degraded one.
const LocalFileName = "brainnotes.local.toml"

// EnrollmentMarker is the vault-relative path whose existence marks the vault
// as enrolled in two-way synchronization.
//
// Loading needs the yes/no answer and nothing else, so it stats this path and
// never reads it. The vault and device identities, the enrollment epoch, and
// the device records live inside the file and belong to the synchronization
// work, which owns its contents.
const EnrollmentMarker = ".bnotes/sync/enrollment.json"

// EnrollmentState reports whether a vault participates in synchronization.
type EnrollmentState string

const (
	// EnrollmentNone is a vault that synchronizes with nothing. Every vault is
	// this until it is enrolled, and a legacy single-file configuration is only
	// ever loaded in this state.
	EnrollmentNone EnrollmentState = "none"
	// EnrollmentEnrolled is a vault that exchanges changes with a peer. Field
	// ownership is enforced here and only here.
	EnrollmentEnrolled EnrollmentState = "enrolled"
)

// Sources carries the exact bytes every configuration layer contributed.
type Sources struct {
	// Portable is the brainnotes.toml body. It is always present: that file is
	// the vault-root marker, so a vault without it was never resolved.
	Portable []byte
	// Local is the brainnotes.local.toml body. A nil slice means the overlay is
	// absent; a non-nil empty slice means the file exists and is empty.
	//
	// The distinction is deliberate and is carried all the way into the
	// fingerprint. An absent overlay is a vault in its legacy shape. An empty
	// one is a file a person created — probably about to fill in, possibly just
	// emptied — and a reload that could not tell the two apart would report
	// "nothing changed" across a deletion that restored a portable value.
	Local []byte
}

// LocalPresent reports whether the vault carries a brainnotes.local.toml.
func (s Sources) LocalPresent() bool { return s.Local != nil }

// Fingerprint identifies these exact layers without decoding them. A caller
// that wants to know "did the configuration change" before it finds out whether
// the configuration is even valid asks here, so a version that fails to decode
// is still identified by its bytes rather than only by its error.
func (s Sources) Fingerprint() Fingerprint { return fingerprintSources(s) }

// Fingerprint identifies the exact bytes each configuration layer contributed,
// and the pair as a whole.
//
// Composite is the value a caller compares when it wants to know whether the
// configuration changed: it covers both layers and the presence of the overlay,
// so creating an empty brainnotes.local.toml, filling it in, and deleting it
// again are three distinct fingerprints.
//
// It covers *bytes*, and nothing else. Enrolling a vault can turn a
// configuration that loads into one that is refused with neither file edited, so
// a caller that skips the decode entirely on an unchanged composite would keep
// serving a configuration the vault no longer accepts. Skip the *publication*,
// not the load.
type Fingerprint struct {
	// Portable is the hex SHA-256 of the brainnotes.toml bytes.
	Portable string
	// Local is the hex SHA-256 of the brainnotes.local.toml bytes, and empty
	// when the overlay is absent. An empty overlay hashes to the SHA-256 of no
	// bytes, which is not the empty string, so absent and empty differ here too.
	Local string
	// LocalPresent records whether the overlay existed at load time.
	LocalPresent bool
	// Composite covers both layers and the presence of the overlay.
	Composite string
}

// fingerprintVersion is mixed into the composite so a later change to what a
// fingerprint covers cannot silently compare equal to one computed under the
// old rule.
const fingerprintVersion = "brainnotes-config-v1"

// layerPath returns the absolute path of one configuration layer.
func layerPath(root string, layer Layer) string {
	return filepath.Join(root, layer.FileName())
}

// LayerPath returns the absolute path of one configuration layer in the vault
// at root.
//
// It exists for the callers that write: a write has to name a file, and the
// file a key belongs in is a fact about the ownership table rather than
// something each caller should re-derive by joining a name onto a root. An
// overlay that does not exist yet still has a path, which is what lets a write
// create it.
func LayerPath(root string, layer Layer) string { return layerPath(root, layer) }

// OwnerPath returns the file that owns key for this vault, and whether key
// names a configuration field at all.
//
// This is deliberately not "the file the value came from". A key no file
// assigns has no source and still has an owner, and that is exactly the case a
// first write is in. Routing a write by provenance would make the destination
// depend on the vault's current contents; routing it by ownership makes the
// same key land in the same file on every machine.
func (c *Config) OwnerPath(key string) (string, bool) {
	if c == nil {
		return "", false
	}
	layer, ok := KeyLayer(key)
	if !ok {
		return "", false
	}
	return layerPath(c.Vault, layer), true
}

// requireRoot refuses an empty vault root.
//
// Without it every layerPath would come back relative and the loader would read
// ./brainnotes.local.toml out of whatever directory the process happens to be
// sitting in — the ambient read this project's isolation rules exist to stop,
// arriving through a zero value rather than through a flag.
func requireRoot(root string) error {
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("config: no vault root")
	}
	return nil
}

// VaultEnrollment reports whether root is enrolled in synchronization.
//
// A marker that cannot be stat'ed for a reason other than absence is an error
// rather than a silent "not enrolled": treating an unreadable marker as absent
// would quietly turn off the field-ownership rules on a vault that is in fact
// synchronizing.
func VaultEnrollment(root string) (EnrollmentState, error) {
	if err := requireRoot(root); err != nil {
		return EnrollmentNone, err
	}
	path := filepath.Join(root, filepath.FromSlash(EnrollmentMarker))
	switch _, err := os.Stat(path); {
	case err == nil:
		return EnrollmentEnrolled, nil
	case os.IsNotExist(err):
		return EnrollmentNone, nil
	default:
		return EnrollmentNone, fmt.Errorf("config: stat %s: %w", path, err)
	}
}

// ReadSources reads every configuration layer a vault carries. An absent
// overlay leaves Sources.Local nil; an unreadable one is an error, because a
// file that exists and cannot be read is not the same answer as no file.
func ReadSources(root string) (Sources, error) {
	if err := requireRoot(root); err != nil {
		return Sources{}, err
	}
	portable, err := os.ReadFile(layerPath(root, LayerPortable))
	if err != nil {
		return Sources{}, fmt.Errorf("config: read %s: %w", layerPath(root, LayerPortable), err)
	}
	local, err := readLocalSource(root)
	if err != nil {
		return Sources{}, err
	}
	return Sources{Portable: portable, Local: local}, nil
}

// readLocalSource reads the overlay, returning a nil slice when it is absent.
func readLocalSource(root string) ([]byte, error) {
	if err := requireRoot(root); err != nil {
		return nil, err
	}
	path := layerPath(root, LayerLocal)
	source, err := os.ReadFile(path)
	switch {
	case err == nil:
		// os.ReadFile returns a non-nil empty slice for an empty file, which is
		// exactly the "present but empty" signal Sources documents.
		if source == nil {
			source = []byte{}
		}
		return source, nil
	case os.IsNotExist(err):
		return nil, nil
	default:
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
}

// DecodeSources builds one configuration from explicit layer bytes for a known
// vault root. Load and Decode are both thin wrappers around it, so every entry
// point applies the same layering, ownership, defaults, and validation.
func DecodeSources(root string, origin VaultOrigin, src Sources) (*Config, error) {
	if err := requireRoot(root); err != nil {
		return nil, err
	}
	enrollment, err := VaultEnrollment(root)
	if err != nil {
		return nil, err
	}
	return decodeSourcesAs(root, origin, src, enrollment)
}

// decodeSourcesAs is DecodeSources with the enrollment state supplied rather
// than read from the vault.
//
// Only the migration planner passes anything but the vault's real state, and it
// passes EnrollmentNone for one narrowly bounded purpose: reading what the
// configuration *means* before the move, on a vault whose files are misplaced
// and therefore refused. Ownership is a rule about which file may hold a key,
// not about what any key is worth, so suspending it cannot change the values
// that comparison is about — and the migrated result is still decoded under the
// vault's real state, so nothing this produces reaches disk without having
// satisfied the rule for real.
func decodeSourcesAs(root string, origin VaultOrigin, src Sources, enrollment EnrollmentState) (*Config, error) {
	portable, portableMeta, err := decodeLayer(layerPath(root, LayerPortable), src.Portable)
	if err != nil {
		return nil, err
	}
	local := &Config{}
	// A zero MetaData has no keys, which is exactly "the overlay assigned
	// nothing" — the same answer an absent overlay gives.
	var localMeta toml.MetaData
	if src.LocalPresent() {
		local, localMeta, err = decodeLayer(layerPath(root, LayerLocal), src.Local)
		if err != nil {
			return nil, err
		}
	}

	portableAssigned := assignedFields(portableMeta)
	localAssigned := assignedFields(localMeta)
	if err := checkOwnership(root, enrollment, portableAssigned, localAssigned); err != nil {
		return nil, err
	}

	cfg := portable
	describeSources(cfg, root, origin, src, enrollment)
	cfg.Provenance = overlay(cfg, local, portableAssigned, localAssigned)

	if err := validateTemplatesDir(cfg.Templates.Dir); err != nil {
		return nil, err
	}
	applyDefaults(cfg)
	if err := validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// describeSources records where the configuration came from. Every field it
// sets is runtime-only (`toml:"-"`), so decoding can never have populated one.
func describeSources(cfg *Config, root string, origin VaultOrigin, src Sources, enrollment EnrollmentState) {
	cfg.Vault = root
	cfg.VaultOrigin = origin
	cfg.SourcePath = layerPath(root, LayerPortable)
	if src.LocalPresent() {
		cfg.LocalSourcePath = layerPath(root, LayerLocal)
	}
	cfg.Enrollment = enrollment
	cfg.Fingerprint = src.Fingerprint()
}

// overlay copies every field the local file assigned onto the portable
// configuration and reports which layer each assigned field came from.
//
// The local file wins wherever both assign the same field. On an enrolled vault
// that can only happen for a field the local file owns, because checkOwnership
// has already refused anything else; on an unenrolled one it is the deliberate
// machine-level override.
func overlay(dst, local *Config, portableAssigned, localAssigned map[string]bool) map[string]Layer {
	provenance := make(map[string]Layer, len(portableAssigned)+len(localAssigned))
	for _, spec := range fieldSpecs {
		if portableAssigned[spec.key] {
			provenance[spec.key] = LayerPortable
		}
		if localAssigned[spec.key] {
			spec.copy(dst, local)
			provenance[spec.key] = LayerLocal
		}
	}
	return provenance
}

// decodeLayer parses one configuration file. path is used for every message, so
// an unknown key reported to the user names the file that actually holds it.
func decodeLayer(path string, source []byte) (*Config, toml.MetaData, error) {
	cfg := &Config{}
	meta, err := toml.Decode(string(source), cfg)
	if err != nil {
		return nil, meta, fmt.Errorf("config: parse %s: %w", path, err)
	}
	if err := rejectUnknownKeys(path, meta); err != nil {
		return nil, meta, err
	}
	return cfg, meta, nil
}

// rejectUnknownKeys fails the load on any key the schema does not define,
// pointing at the replacement where one exists.
func rejectUnknownKeys(path string, meta toml.MetaData) error {
	undecoded := meta.Undecoded()
	if len(undecoded) == 0 {
		return nil
	}
	keys := make([]string, len(undecoded))
	for i, k := range undecoded {
		keys[i] = k.String()
		if err := removedKeyError(path, keys[i]); err != nil {
			return err
		}
	}
	return fmt.Errorf("config: %s: unknown keys: %s", path, strings.Join(keys, ", "))
}

// removedKeyError maps a key this schema used to have onto its replacement, so
// an upgrade reports the migration rather than an anonymous unknown key.
func removedKeyError(path, key string) error {
	if key == "vault" {
		return fmt.Errorf(
			"config: %s: the `vault` key was removed; the vault root is the directory holding %s",
			path, FileName)
	}
	if legacy := legacyPropertyKey(key); legacy != "" {
		return fmt.Errorf(
			"config: %s: `%s` was replaced by unified [frontmatter.properties.<name>] definitions with type/items/required/allowed",
			path, legacy)
	}
	return nil
}

// fingerprintSources hashes each layer and the pair.
func fingerprintSources(src Sources) Fingerprint {
	fp := Fingerprint{
		Portable:     hashSource(src.Portable),
		LocalPresent: src.LocalPresent(),
	}
	record := fingerprintVersion + "\n" +
		string(LayerPortable) + "\x00" + fp.Portable + "\n" +
		string(LayerLocal) + "\x00"
	if fp.LocalPresent {
		fp.Local = hashSource(src.Local)
		record += fp.Local
	} else {
		// A word no hex digest can spell, so "no overlay" cannot collide with
		// the hash of any overlay that might exist.
		record += "absent"
	}
	fp.Composite = hashSource([]byte(record + "\n"))
	return fp
}

// hashSource is the hex SHA-256 encoding used for every configuration digest.
// It matches vault.HashBytes byte for byte; internal/config cannot import
// internal/vault (vault imports config), so the two-line encoding is restated
// rather than shared.
func hashSource(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// SourceOf reports which configuration file assigned key, and whether any file
// did. A key no file assigned holds its default.
func (c *Config) SourceOf(key string) (Layer, bool) {
	if c == nil {
		return "", false
	}
	layer, ok := c.Provenance[key]
	return layer, ok
}

func cloneProvenance(in map[string]Layer) map[string]Layer {
	if in == nil {
		return nil
	}
	out := make(map[string]Layer, len(in))
	for key, layer := range in {
		out[key] = layer
	}
	return out
}
