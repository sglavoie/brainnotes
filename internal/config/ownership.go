package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// Layer names one of the two configuration files a vault may carry.
type Layer string

const (
	// LayerPortable is brainnotes.toml, the vault-root marker. It describes the
	// vault itself and is meant to travel with it.
	LayerPortable Layer = "portable"
	// LayerLocal is brainnotes.local.toml, the optional overlay. It describes
	// this machine and never travels.
	LayerLocal Layer = "local"
)

// FileName returns the file that carries the layer, relative to the vault root.
func (l Layer) FileName() string {
	if l == LayerLocal {
		return LocalFileName
	}
	return FileName
}

// fieldSpec declares which file owns one configuration key and how to carry its
// decoded value from one Config into another.
//
// The key is a TOML key path. A spec whose value is a table or an array of
// tables (`frontmatter.properties`, `new.routes`, `views`) governs every key
// underneath it and is overlaid whole: a local file that declares one property
// replaces the portable file's property table rather than merging into it, so
// what the merged configuration says is always exactly what one of the two
// files says.
type fieldSpec struct {
	key   string
	layer Layer
	copy  func(dst, src *Config)
}

// fieldSpecs is the ownership table: every configuration key, exactly once,
// with the file that owns it.
//
// A key is local when its value names something that exists only on this
// machine — a binary to execute, an address to bind, or the arguments and
// timeout that go with one of those. Everything else describes the vault and is
// portable, including `autosave.min_interval`: that is a policy about how often
// the vault's own safety history gains a commit, expressed in the vault's own
// units, and nothing in it identifies a machine. Keeping it portable also keeps
// `bn config set`, which writes the portable file, writing to the file that
// owns the only key it accepts.
//
// TestFieldSpecs_CoverEveryConfigKey proves this table names every decodable
// key and no others, so a new field cannot be added without deciding which file
// owns it.
var fieldSpecs = []fieldSpec{
	{"ignore", LayerPortable, func(dst, src *Config) { dst.Ignore = src.Ignore }},
	{"templates.dir", LayerPortable, func(dst, src *Config) { dst.Templates.Dir = src.Templates.Dir }},
	{"templates.default_extension", LayerPortable, func(dst, src *Config) {
		dst.Templates.DefaultExtension = src.Templates.DefaultExtension
	}},
	{"frontmatter.properties", LayerPortable, func(dst, src *Config) {
		dst.Frontmatter.Properties = src.Frontmatter.Properties
	}},
	{"fzf.args", LayerLocal, func(dst, src *Config) { dst.Fzf.Args = src.Fzf.Args }},
	{"open.editor", LayerLocal, func(dst, src *Config) { dst.Open.Editor = src.Open.Editor }},
	{"serve.addr", LayerLocal, func(dst, src *Config) { dst.Serve.Addr = src.Serve.Addr }},
	{"serve.open_browser", LayerLocal, func(dst, src *Config) { dst.Serve.OpenBrowser = src.Serve.OpenBrowser }},
	{"new.routes", LayerPortable, func(dst, src *Config) { dst.New.Routes = src.New.Routes }},
	{"related.metadata_properties", LayerPortable, func(dst, src *Config) {
		dst.Related.MetadataProperties = src.Related.MetadataProperties
	}},
	{"brief.exclude_folders", LayerPortable, func(dst, src *Config) {
		dst.Brief.ExcludeFolders = src.Brief.ExcludeFolders
	}},
	{"brief.exclude_types", LayerPortable, func(dst, src *Config) { dst.Brief.ExcludeTypes = src.Brief.ExcludeTypes }},
	{"capture.inbox_note", LayerPortable, func(dst, src *Config) { dst.Capture.InboxNote = src.Capture.InboxNote }},
	{"capture.heading", LayerPortable, func(dst, src *Config) { dst.Capture.Heading = src.Capture.Heading }},
	{"agent.command", LayerLocal, func(dst, src *Config) { dst.Agent.Command = src.Agent.Command }},
	{"agent.timeout_seconds", LayerLocal, func(dst, src *Config) {
		dst.Agent.TimeoutSeconds = src.Agent.TimeoutSeconds
	}},
	{"cockpit.disabled_rules", LayerPortable, func(dst, src *Config) {
		dst.Cockpit.DisabledRules = src.Cockpit.DisabledRules
	}},
	{"cockpit.priority", LayerPortable, func(dst, src *Config) { dst.Cockpit.Priority = src.Cockpit.Priority }},
	{"cockpit.long_tail_limit", LayerPortable, func(dst, src *Config) {
		dst.Cockpit.LongTailLimit = src.Cockpit.LongTailLimit
	}},
	{"cockpit.long_tail_max_count", LayerPortable, func(dst, src *Config) {
		dst.Cockpit.LongTailMaxCount = src.Cockpit.LongTailMaxCount
	}},
	{"cockpit.long_tail_head_factor", LayerPortable, func(dst, src *Config) {
		dst.Cockpit.LongTailHeadFactor = src.Cockpit.LongTailHeadFactor
	}},
	{"cockpit.long_tail_ignore_properties", LayerPortable, func(dst, src *Config) {
		dst.Cockpit.LongTailIgnoreProperties = src.Cockpit.LongTailIgnoreProperties
	}},
	{"cockpit.daily_limit", LayerPortable, func(dst, src *Config) { dst.Cockpit.DailyLimit = src.Cockpit.DailyLimit }},
	{"cockpit.review_date_property", LayerPortable, func(dst, src *Config) {
		dst.Cockpit.ReviewDateProperty = src.Cockpit.ReviewDateProperty
	}},
	{"cockpit.review_statuses", LayerPortable, func(dst, src *Config) {
		dst.Cockpit.ReviewStatuses = src.Cockpit.ReviewStatuses
	}},
	{"cockpit.wip_statuses", LayerPortable, func(dst, src *Config) {
		dst.Cockpit.WIPStatuses = src.Cockpit.WIPStatuses
	}},
	{"cockpit.max_wip", LayerPortable, func(dst, src *Config) { dst.Cockpit.MaxWIP = src.Cockpit.MaxWIP }},
	{"cockpit.stale_status_days", LayerPortable, func(dst, src *Config) {
		dst.Cockpit.StaleStatusDays = src.Cockpit.StaleStatusDays
	}},
	{"cockpit.max_captures", LayerPortable, func(dst, src *Config) { dst.Cockpit.MaxCaptures = src.Cockpit.MaxCaptures }},
	{"cockpit.hub_type_values", LayerPortable, func(dst, src *Config) {
		dst.Cockpit.HubTypeValues = src.Cockpit.HubTypeValues
	}},
	{"cockpit.hub_empty_headings", LayerPortable, func(dst, src *Config) {
		dst.Cockpit.HubEmptyHeadings = src.Cockpit.HubEmptyHeadings
	}},
	{"cockpit.sensitive_scan", LayerPortable, func(dst, src *Config) {
		dst.Cockpit.SensitiveScan = src.Cockpit.SensitiveScan
	}},
	{"cockpit.sensitive_ignore_paths", LayerPortable, func(dst, src *Config) {
		dst.Cockpit.SensitiveIgnorePaths = src.Cockpit.SensitiveIgnorePaths
	}},
	{"cockpit.sensitive_ignore_patterns", LayerPortable, func(dst, src *Config) {
		dst.Cockpit.SensitiveIgnorePatterns = src.Cockpit.SensitiveIgnorePatterns
	}},
	{"cockpit.agent_command", LayerLocal, func(dst, src *Config) {
		dst.Cockpit.AgentCommand = src.Cockpit.AgentCommand
	}},
	{"cockpit.agent_timeout_seconds", LayerLocal, func(dst, src *Config) {
		dst.Cockpit.AgentTimeoutSeconds = src.Cockpit.AgentTimeoutSeconds
	}},
	{"autosave.min_interval", LayerPortable, func(dst, src *Config) {
		dst.Autosave.MinInterval = src.Autosave.MinInterval
	}},
	{"attachments.dir", LayerPortable, func(dst, src *Config) { dst.Attachments.Dir = src.Attachments.Dir }},
	{"attachments.max_file_bytes", LayerPortable, func(dst, src *Config) {
		dst.Attachments.MaxFileBytes = src.Attachments.MaxFileBytes
	}},
	{"views", LayerPortable, func(dst, src *Config) { dst.Views = src.Views }},
}

// governs reports whether the decoded TOML key path defined belongs to this
// field. A table spec also governs everything nested under it.
func (s fieldSpec) governs(defined string) bool {
	return defined == s.key || strings.HasPrefix(defined, s.key+".")
}

// specFor returns the field a decoded TOML key path belongs to. A key that
// names a table on the way down to a field — `serve` for `serve.addr` — belongs
// to no field and reports false. A key that names a *table-valued* field is a
// different case and is handled by assignedFields, which is the only caller that
// can tell a header apart from a value.
func specFor(defined string) (fieldSpec, bool) {
	for _, spec := range fieldSpecs {
		if spec.governs(defined) {
			return spec, true
		}
	}
	return fieldSpec{}, false
}

// KeyLayer reports which configuration file owns key, and whether key names a
// configuration field at all.
func KeyLayer(key string) (Layer, bool) {
	spec, ok := specFor(key)
	if !ok {
		return "", false
	}
	return spec.layer, true
}

// LayerKeys returns the configuration keys the named file owns, sorted. It is
// the machine-readable form of the portable/local split, for the callers that
// have to decide what may travel between vaults.
func LayerKeys(layer Layer) []string {
	var keys []string
	for _, spec := range fieldSpecs {
		if spec.layer == layer {
			keys = append(keys, spec.key)
		}
	}
	sort.Strings(keys)
	return keys
}

// tomlTypeHash and tomlTypeArrayHash are the types BurntSushi/toml reports for
// a key that is a table header or an array-of-tables element rather than a
// value.
const (
	tomlTypeHash      = "Hash"
	tomlTypeArrayHash = "ArrayHash"
)

// assignedFields reduces one file's decoded metadata to the set of fields it
// actually assigned.
//
// A key whose TOML type is a table or an array of tables is a *header*, not an
// assignment. `[serve]` with nothing under it is the obvious case, but the
// load-bearing one is a table-valued field: `[frontmatter.properties]` with
// every example block commented out, exactly as the starter template ships it,
// decodes to an empty map that would otherwise be overlaid onto — and erase —
// the vault's real declarations, with a configuration that still loads cleanly
// and a `bn set` that quietly stops splitting lists. So only a key holding a
// scalar or an array counts, credited to the spec governing it: `views.name`
// assigns `views`, while a bare `[[views]]` assigns nothing.
//
// An *inline* empty table is the same type and therefore the same answer, so the
// rule to state is the strong one: a table-valued field can be replaced but
// never emptied from the overlay, in any spelling. (An array-valued field is
// unaffected — `views = []` is an Array, so it clears.) That is the safe
// direction to be wrong in: the overlay can always be deleted, but a property
// table erased behind a configuration that still loads is invisible.
func assignedFields(meta toml.MetaData) map[string]bool {
	keys := meta.Keys()
	out := make(map[string]bool, len(keys))
	for _, key := range keys {
		switch meta.Type(key...) {
		case tomlTypeHash, tomlTypeArrayHash:
			continue
		}
		if spec, ok := specFor(key.String()); ok {
			out[spec.key] = true
		}
	}
	return out
}

// checkOwnership rejects a key written to the file that does not own it.
//
// It applies only to an enrolled vault, and that is the whole point of the
// enrollment test rather than a softening of it. Misplacement is a
// synchronization fault: a portable key parked in the local file never reaches
// the peer vault, and a local key left in the portable file does reach it and
// overwrites the peer's own editor, address, or agent command. A vault that is
// not enrolled synchronizes nothing, so neither failure exists there — and
// every legacy single-file configuration is precisely a portable file carrying
// local keys, so rejecting those would break every vault in existence.
func checkOwnership(root string, enrollment EnrollmentState, portable, local map[string]bool) error {
	if enrollment != EnrollmentEnrolled {
		return nil
	}
	if err := misplacedError(root, LayerPortable, portable); err != nil {
		return err
	}
	return misplacedError(root, LayerLocal, local)
}

// misplacedError names every field the given file assigned that the other file
// owns.
func misplacedError(root string, layer Layer, assigned map[string]bool) error {
	var misplaced []string
	for _, spec := range fieldSpecs {
		if spec.layer != layer && assigned[spec.key] {
			misplaced = append(misplaced, spec.key)
		}
	}
	if len(misplaced) == 0 {
		return nil
	}
	sort.Strings(misplaced)
	other := LayerPortable
	if layer == LayerPortable {
		other = LayerLocal
	}
	return fmt.Errorf(
		"config: %s: this vault is enrolled in synchronization, so these keys belong in %s instead: %s",
		layerPath(root, layer), other.FileName(), strings.Join(misplaced, ", "))
}
