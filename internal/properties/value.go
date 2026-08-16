// Package properties builds the read-only property intelligence index that the
// browser workbench renders and that property mutation surfaces validate
// against. It classifies every frontmatter value into a typed, JSON-safe
// representation, aggregates per-property statistics, and infers conservative
// schema proposals.
package properties

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/wikilink"
)

// Kind aliases the configured kind vocabulary so an observed kind and a
// declared kind are never two spellings of the same idea.
type Kind = config.PropertyKind

// KindUnsupported labels a value that has no supported scalar spelling: YAML
// null, a mapping, or a nested list. Such values are reported by label only and
// are never stringified into something a write could reproduce.
const KindUnsupported Kind = "unsupported"

const (
	dateLayout     = "2006-01-02"
	datetimeLayout = time.RFC3339
)

// Value is one frontmatter value rendered for read-only transport. Kind is the
// observed kind, Text is the display spelling, Value carries the JSON scalar
// for supported scalars, and Items carries the elements of a list.
type Value struct {
	Kind      Kind    `json:"kind"`
	Text      string  `json:"text"`
	Value     any     `json:"value,omitempty"`
	Items     []Value `json:"items,omitempty"`
	Supported bool    `json:"supported"`
}

// IsList reports whether the value was stored as a YAML sequence.
func (v Value) IsList() bool { return v.Kind == config.KindList }

// Logical returns the logical values the value contributes to the catalog: the
// elements of a list, or the value itself for a scalar.
func (v Value) Logical() []Value {
	if v.IsList() {
		return v.Items
	}
	return []Value{v}
}

// key identifies one logical value for distinct counting. Kind is part of the
// key so the integer 1 and the string "1" stay distinct values.
func (v Value) key() string { return string(v.Kind) + "\x00" + v.Text }

// ClassifyProperty renders the value stored at key in front. front supplies the
// date-only spelling metadata that separates a date from a datetime.
func ClassifyProperty(front *frontmatter.Map, key string) (Value, bool) {
	if front == nil {
		return Value{}, false
	}
	raw, ok := front.Get(key)
	if !ok {
		return Value{}, false
	}
	return classifyValue(front, key, raw), true
}

func classifyValue(front *frontmatter.Map, key string, raw any) Value {
	switch items := raw.(type) {
	case []any:
		return classifyList(front, key, items)
	case []string:
		anyItems := make([]any, 0, len(items))
		for _, item := range items {
			anyItems = append(anyItems, item)
		}
		return classifyList(front, key, anyItems)
	}
	return classifyScalar(raw, front.TimeIsDateOnly(key))
}

func classifyList(front *frontmatter.Map, key string, items []any) Value {
	values := make([]Value, 0, len(items))
	texts := make([]string, 0, len(items))
	for i, item := range items {
		value := classifyScalar(item, front.ListTimeIsDateOnly(key, i))
		values = append(values, value)
		texts = append(texts, value.Text)
	}
	return Value{
		Kind:      config.KindList,
		Text:      strings.Join(texts, ", "),
		Items:     values,
		Supported: true,
	}
}

func classifyScalar(raw any, dateOnly bool) Value {
	switch v := raw.(type) {
	case string:
		if target, ok := bareWikilinkTarget(v); ok {
			return Value{Kind: config.KindWikilink, Text: target, Value: target, Supported: true}
		}
		return Value{Kind: config.KindString, Text: v, Value: v, Supported: true}
	case time.Time:
		if dateOnly {
			text := v.Format(dateLayout)
			return Value{Kind: config.KindDate, Text: text, Value: text, Supported: true}
		}
		text := v.Format(datetimeLayout)
		return Value{Kind: config.KindDatetime, Text: text, Value: text, Supported: true}
	case int:
		return Value{Kind: config.KindInt, Text: strconv.Itoa(v), Value: v, Supported: true}
	case float64:
		return Value{Kind: config.KindFloat, Text: formatFloat(v), Value: v, Supported: true}
	case bool:
		return Value{Kind: config.KindBool, Text: strconv.FormatBool(v), Value: v, Supported: true}
	}
	return Value{Kind: KindUnsupported, Text: unsupportedLabel(raw)}
}

// bareWikilinkTarget reports the link target when s is exactly one wikilink and
// nothing else, which is how a wikilink-typed property stores its value.
func bareWikilinkTarget(s string) (string, bool) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return "", false
	}
	links := wikilink.Parse(trimmed)
	if len(links) != 1 || links[0].Start != 0 || links[0].End != len(trimmed) {
		return "", false
	}
	target := strings.TrimSpace(links[0].Target)
	if target == "" {
		return "", false
	}
	return target, true
}

// unsupportedLabel names a value that has no supported scalar spelling.
func unsupportedLabel(raw any) string {
	switch raw.(type) {
	case nil:
		return "<null>"
	case map[string]any:
		return "<map>"
	case []any, []string:
		return "<nested list>"
	}
	return "<unsupported " + fmt.Sprintf("%T", raw) + ">"
}

func formatFloat(f float64) string {
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return s
	}
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}
