package config

import (
	"sort"
	"strings"
)

// ScalarKinds lists every kind a scalar property (or a list's items) may use.
var ScalarKinds = []PropertyKind{
	KindString,
	KindWikilink,
	KindDate,
	KindDatetime,
	KindInt,
	KindFloat,
	KindBool,
}

// ValidScalarKind reports whether k names a supported scalar kind.
func ValidScalarKind(k PropertyKind) bool {
	for _, candidate := range ScalarKinds {
		if k == candidate {
			return true
		}
	}
	return false
}

// ValidPropertyKind reports whether k names a supported property type.
func ValidPropertyKind(k PropertyKind) bool {
	return k == KindList || ValidScalarKind(k)
}

// IsList reports whether the definition describes a list property.
func (d PropertyDefinition) IsList() bool { return d.Type == KindList }

// ScalarKind returns the kind each stored value must have: Items for a list,
// Type otherwise.
func (d PropertyDefinition) ScalarKind() PropertyKind {
	if d.IsList() {
		return d.Items
	}
	return d.Type
}

// IsWikilink reports whether the definition's values are wikilink targets.
func (d PropertyDefinition) IsWikilink() bool { return d.ScalarKind() == KindWikilink }

// Summary renders the definition as a short human-readable string.
func (d PropertyDefinition) Summary() string {
	s := "type: " + string(d.Type)
	if d.IsList() && d.Items != "" {
		s += " of " + string(d.Items)
	}
	if len(d.Allowed) > 0 {
		s += ", allowed: " + strings.Join(d.Allowed, ", ")
	}
	return s
}

// Definition returns the definition configured for key.
func (c FrontmatterConfig) Definition(key string) (PropertyDefinition, bool) {
	def, ok := c.Properties[key]
	return def, ok
}

// IsList reports whether key is defined as a list property.
func (c FrontmatterConfig) IsList(key string) bool {
	def, ok := c.Properties[key]
	return ok && def.IsList()
}

// IsWikilink reports whether key stores wikilink targets.
func (c FrontmatterConfig) IsWikilink(key string) bool {
	def, ok := c.Properties[key]
	return ok && def.IsWikilink()
}

// PropertyNames returns every configured property name in sorted order.
func (c FrontmatterConfig) PropertyNames() []string {
	out := make([]string, 0, len(c.Properties))
	for key := range c.Properties {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
