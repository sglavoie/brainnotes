package config

import (
	"fmt"
	"sort"
	"strings"
)

// SettingKind names the shape of one editable setting's value.
//
// There are four because that is what the portable keys actually hold. A kind
// is not a hint for a form widget: it is what decides how a value is rendered
// into TOML and what a caller is allowed to send, so a client cannot post a
// string for a count and have the patcher quote it into a body the loader would
// then refuse.
type SettingKind string

const (
	SettingString     SettingKind = "string"
	SettingBool       SettingKind = "bool"
	SettingInt        SettingKind = "int"
	SettingStringList SettingKind = "string_list"
)

// SettingSpec describes one portable configuration key that can be edited as a
// typed value.
//
// Summary is the one-line description an editing surface shows beside the
// field. It lives here rather than in the surface because the same sentence has
// to be true for the CLI and the browser, and a second copy of it is a second
// thing to get out of date.
type SettingSpec struct {
	Key     string
	Kind    SettingKind
	Summary string

	read func(*Config) SettingValue
}

// Table returns the TOML table the key lives under, empty for a root-level key.
func (s SettingSpec) Table() string {
	if i := strings.LastIndex(s.Key, "."); i >= 0 {
		return s.Key[:i]
	}
	return ""
}

// Name returns the key's last segment: the bare key as it is spelled inside its
// table.
func (s SettingSpec) Name() string {
	if i := strings.LastIndex(s.Key, "."); i >= 0 {
		return s.Key[i+1:]
	}
	return s.Key
}

// SettingValue is one typed value for an editable setting. Exactly one of the
// four payload fields is meaningful, named by Kind.
type SettingValue struct {
	Kind   SettingKind
	Text   string
	Number int64
	Flag   bool
	Items  []string
}

// StringSetting, IntSetting, BoolSetting and ListSetting build a value of each
// kind. They exist so a caller never has to remember which payload field goes
// with which kind, which is the one way to build a SettingValue that renders as
// something other than what it says it is.
func StringSetting(text string) SettingValue { return SettingValue{Kind: SettingString, Text: text} }
func IntSetting(n int64) SettingValue        { return SettingValue{Kind: SettingInt, Number: n} }
func BoolSetting(flag bool) SettingValue     { return SettingValue{Kind: SettingBool, Flag: flag} }

// ListSetting copies items so a caller's slice cannot be aliased into a value
// that is compared against the file later.
func ListSetting(items []string) SettingValue {
	return SettingValue{Kind: SettingStringList, Items: append([]string(nil), items...)}
}

// editableSettings is every portable key an editing surface may write, with the
// type its value has.
//
// It is deliberately a second table beside fieldSpecs rather than a column on
// it. fieldSpecs answers "which file owns this key", which every vault needs;
// this answers "may a person retype this value in a form", which is a narrower
// question with a different answer for three portable keys — see
// settingsOwnedElsewhere. TestEditableSettings_CoverEveryPortableKey proves the
// two tables agree, so a key added to fieldSpecs cannot reach a browser form
// without someone deciding it belongs there.
var editableSettings = []SettingSpec{
	{Key: "ignore", Kind: SettingStringList,
		Summary: "Extra vault-relative paths skipped by every walk.",
		read:    func(c *Config) SettingValue { return ListSetting(c.Ignore) }},
	{Key: "templates.dir", Kind: SettingString,
		Summary: "Vault-relative folder holding note templates.",
		read:    func(c *Config) SettingValue { return StringSetting(c.Templates.Dir) }},
	{Key: "templates.default_extension", Kind: SettingString,
		Summary: "Extension appended to a template name that carries none.",
		read:    func(c *Config) SettingValue { return StringSetting(c.Templates.DefaultExtension) }},
	{Key: "related.metadata_properties", Kind: SettingStringList,
		Summary: "Properties whose values feed metadata similarity. An empty list disables the signal.",
		read:    func(c *Config) SettingValue { return ListSetting(c.Related.MetadataProperties) }},
	{Key: "brief.exclude_folders", Kind: SettingStringList,
		Summary: "Folder prefixes the daily brief never recommends from.",
		read:    func(c *Config) SettingValue { return ListSetting(c.Brief.ExcludeFolders) }},
	{Key: "brief.exclude_types", Kind: SettingStringList,
		Summary: "Frontmatter type values the daily brief never recommends.",
		read:    func(c *Config) SettingValue { return ListSetting(c.Brief.ExcludeTypes) }},
	{Key: "capture.inbox_note", Kind: SettingString,
		Summary: "Vault-relative note that captures are appended to.",
		read:    func(c *Config) SettingValue { return StringSetting(c.Capture.InboxNote) }},
	{Key: "capture.heading", Kind: SettingString,
		Summary: "Heading captures are appended under.",
		read:    func(c *Config) SettingValue { return StringSetting(c.Capture.Heading) }},
	{Key: "autosave.min_interval", Kind: SettingString,
		Summary: "Floor between two external autosave commits, or \"off\".",
		read:    func(c *Config) SettingValue { return StringSetting(c.Autosave.MinInterval) }},
	{Key: "attachments.dir", Kind: SettingString,
		Summary: "Vault-relative folder new attachments are filed under.",
		read:    func(c *Config) SettingValue { return StringSetting(c.Attachments.Dir) }},
	{Key: "attachments.max_file_bytes", Kind: SettingInt,
		Summary: "Largest single upload the vault accepts, in bytes.",
		read:    func(c *Config) SettingValue { return IntSetting(c.Attachments.MaxFileBytes) }},
}

// cockpitSettings is the cockpit half of the table, split out because a single
// literal covering every portable key is longer than anything else in this
// package and reads as a wall rather than a list.
var cockpitSettings = []SettingSpec{
	{Key: "cockpit.disabled_rules", Kind: SettingStringList,
		Summary: "Rule kinds suppressed from the hygiene queue.",
		read:    func(c *Config) SettingValue { return ListSetting(c.Cockpit.DisabledRules) }},
	{Key: "cockpit.priority", Kind: SettingStringList,
		Summary: "Rule kinds surfaced first, in order.",
		read:    func(c *Config) SettingValue { return ListSetting(c.Cockpit.Priority) }},
	{Key: "cockpit.long_tail_limit", Kind: SettingInt,
		Summary: "Rarest values per property that become long-tail tasks.",
		read:    func(c *Config) SettingValue { return IntSetting(int64(c.Cockpit.LongTailLimit)) }},
	{Key: "cockpit.long_tail_max_count", Kind: SettingInt,
		Summary: "Highest per-value count still treated as rare.",
		read:    func(c *Config) SettingValue { return IntSetting(int64(c.Cockpit.LongTailMaxCount)) }},
	{Key: "cockpit.long_tail_head_factor", Kind: SettingInt,
		Summary: "How many times the dominant value must exceed a rare one before it is flagged.",
		read:    func(c *Config) SettingValue { return IntSetting(int64(c.Cockpit.LongTailHeadFactor)) }},
	{Key: "cockpit.long_tail_ignore_properties", Kind: SettingStringList,
		Summary: "Properties the long-tail rule never flags.",
		read:    func(c *Config) SettingValue { return ListSetting(c.Cockpit.LongTailIgnoreProperties) }},
	{Key: "cockpit.daily_limit", Kind: SettingInt,
		Summary: "Tasks the daily cockpit view surfaces at once.",
		read:    func(c *Config) SettingValue { return IntSetting(int64(c.Cockpit.DailyLimit)) }},
	{Key: "cockpit.review_date_property", Kind: SettingString,
		Summary: "Property recording when a note was last reviewed.",
		read:    func(c *Config) SettingValue { return StringSetting(c.Cockpit.ReviewDateProperty) }},
	{Key: "cockpit.review_statuses", Kind: SettingStringList,
		Summary: "Status values whose notes are expected to carry a review date.",
		read:    func(c *Config) SettingValue { return ListSetting(c.Cockpit.ReviewStatuses) }},
	{Key: "cockpit.wip_statuses", Kind: SettingStringList,
		Summary: "Status values counted against the work-in-progress limit.",
		read:    func(c *Config) SettingValue { return ListSetting(c.Cockpit.WIPStatuses) }},
	{Key: "cockpit.max_wip", Kind: SettingInt,
		Summary: "Work-in-progress notes allowed before the limit rule fires.",
		read:    func(c *Config) SettingValue { return IntSetting(int64(c.Cockpit.MaxWIP)) }},
	{Key: "cockpit.stale_status_days", Kind: SettingInt,
		Summary: "Age in days after which a status is treated as stale.",
		read:    func(c *Config) SettingValue { return IntSetting(int64(c.Cockpit.StaleStatusDays)) }},
	{Key: "cockpit.max_captures", Kind: SettingInt,
		Summary: "Unprocessed captures allowed before the backlog rule fires. Zero disables it.",
		read:    func(c *Config) SettingValue { return IntSetting(int64(c.Cockpit.MaxCaptures)) }},
	{Key: "cockpit.hub_type_values", Kind: SettingStringList,
		Summary: "Frontmatter type values that identify a hub note.",
		read:    func(c *Config) SettingValue { return ListSetting(c.Cockpit.HubTypeValues) }},
	{Key: "cockpit.hub_empty_headings", Kind: SettingStringList,
		Summary: "Headings that mark a hub as needing synthesis when empty.",
		read:    func(c *Config) SettingValue { return ListSetting(c.Cockpit.HubEmptyHeadings) }},
	{Key: "cockpit.sensitive_scan", Kind: SettingBool,
		Summary: "Whether the cockpit inspects note bodies for secrets.",
		read:    func(c *Config) SettingValue { return BoolSetting(c.Cockpit.SensitiveScan) }},
	{Key: "cockpit.sensitive_ignore_paths", Kind: SettingStringList,
		Summary: "Path prefixes the sensitive-content scanner skips.",
		read:    func(c *Config) SettingValue { return ListSetting(c.Cockpit.SensitiveIgnorePaths) }},
	{Key: "cockpit.sensitive_ignore_patterns", Kind: SettingStringList,
		Summary: "Regular expressions the sensitive-content scanner treats as allowed.",
		read:    func(c *Config) SettingValue { return ListSetting(c.Cockpit.SensitiveIgnorePatterns) }},
}

// settingsOwnedElsewhere names every portable key this typed editor deliberately
// does not accept, and why.
//
// It is data rather than a comment so the coverage test can require each
// portable key to appear in exactly one of the two tables: a key is editable
// here, or someone wrote down which workflow owns it. The alternative — an
// allowlist with no complement — lets a new portable key be silently
// uneditable, which is the same shape of bug as silently editable and harder to
// notice.
var settingsOwnedElsewhere = map[string]string{
	"frontmatter.properties": "edited by the property-definition workflow (/api/properties/schema)",
	"views":                  "edited by the saved-view workflow (/api/views)",
	"new.routes":             "no typed editor yet: a template-name-to-folder map needs its own shape",
}

// settingSpecs is the two halves joined, sorted by key, built once.
var settingSpecs = buildSettingSpecs()

func buildSettingSpecs() []SettingSpec {
	out := make([]SettingSpec, 0, len(editableSettings)+len(cockpitSettings))
	out = append(out, editableSettings...)
	out = append(out, cockpitSettings...)
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// EditableSettings returns every portable key that can be edited as a typed
// value, sorted by key. The returned slice is a copy, so a caller sorting or
// filtering it cannot reorder the table every other caller reads.
func EditableSettings() []SettingSpec {
	return append([]SettingSpec(nil), settingSpecs...)
}

// SettingSpecFor returns the spec for key, and false when key is not an
// editable setting — which is the single gate a write surface consults. A
// machine-local key is not in this table, so asking for one is indistinguishable
// from asking for a key that does not exist, and neither reveals whether the
// overlay assigns it.
func SettingSpecFor(key string) (SettingSpec, bool) {
	for _, spec := range settingSpecs {
		if spec.Key == key {
			return spec, true
		}
	}
	return SettingSpec{}, false
}

// SettingValueOf reads key's effective value out of a loaded configuration.
func SettingValueOf(cfg *Config, key string) (SettingValue, error) {
	spec, ok := SettingSpecFor(key)
	if !ok {
		return SettingValue{}, unknownSettingError(key)
	}
	if cfg == nil {
		return SettingValue{}, fmt.Errorf("config: %s: no configuration is loaded", key)
	}
	return spec.read(cfg), nil
}

func unknownSettingError(key string) error {
	if reason, ok := settingsOwnedElsewhere[key]; ok {
		return fmt.Errorf("config: %s is not editable here: %s", key, reason)
	}
	return fmt.Errorf("config: %q is not an editable setting", key)
}
