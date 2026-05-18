package frontmatter

import (
	"bytes"
	"fmt"
	"sort"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

// Marshal serializes a Document back to bytes. Frontmatter keys are emitted
// in alphabetical order. Values for keys listed in fmCfg.WikilinkProperties
// are auto-wrapped as `[[Value]]` when not already wrapped.
//
// When the Front map is empty, no frontmatter block is emitted and only the
// body is returned. fmCfg may be nil; in that case no wikilink wrapping is
// applied (callers that don't need wrapping can pass nil).
func Marshal(doc Document, fmCfg *config.FrontmatterConfig) ([]byte, error) {
	var out bytes.Buffer
	if !doc.Front.IsEmpty() {
		out.WriteString("---\n")
		yamlBytes, err := marshalMap(doc.Front, fmCfg)
		if err != nil {
			return nil, fmt.Errorf("frontmatter: marshal: %w", err)
		}
		out.Write(yamlBytes)
		out.WriteString("---\n")
	}
	out.WriteString(doc.Body)
	return out.Bytes(), nil
}

// marshalMap encodes the map as a sorted YAML mapping, applying wikilink
// wrapping where appropriate.
func marshalMap(m Map, fmCfg *config.FrontmatterConfig) ([]byte, error) {
	wikilinkSet := make(map[string]bool)
	if fmCfg != nil {
		for _, k := range fmCfg.WikilinkProperties {
			wikilinkSet[k] = true
		}
	}

	keys := m.Keys()
	sorted := make([]string, len(keys))
	copy(sorted, keys)
	sort.Strings(sorted)

	root := &yaml.Node{Kind: yaml.MappingNode}
	for _, k := range sorted {
		v, _ := m.Get(k)
		if wikilinkSet[k] {
			v = applyWikilinkWrap(v)
		}
		keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: k}
		valNode, err := encodeValue(v)
		if err != nil {
			return nil, fmt.Errorf("encode %q: %w", k, err)
		}
		root.Content = append(root.Content, keyNode, valNode)
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// encodeValue builds a yaml.Node for v, using block style for sequences so
// the output is line-oriented (one item per line). Date-only timestamps are
// emitted as `YYYY-MM-DD` rather than a full RFC3339 datetime so they
// round-trip with the vault's existing convention.
func encodeValue(v any) (*yaml.Node, error) {
	if t, ok := v.(time.Time); ok {
		return encodeTime(t), nil
	}
	if list, ok := v.([]any); ok {
		n := &yaml.Node{Kind: yaml.SequenceNode}
		if len(list) == 0 {
			n.Style = yaml.FlowStyle
		}
		for _, item := range list {
			child, err := encodeValue(item)
			if err != nil {
				return nil, err
			}
			n.Content = append(n.Content, child)
		}
		return n, nil
	}
	n := &yaml.Node{}
	if err := n.Encode(v); err != nil {
		return nil, err
	}
	forceBlockStyle(n)
	return n, nil
}

// encodeTime emits a date-only scalar when the time has no time-of-day
// component and is in UTC; otherwise emits a full RFC3339 timestamp.
func encodeTime(t time.Time) *yaml.Node {
	if t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0 && t.Nanosecond() == 0 {
		_, offset := t.Zone()
		if offset == 0 {
			return &yaml.Node{
				Kind:  yaml.ScalarNode,
				Tag:   "!!timestamp",
				Value: t.Format("2006-01-02"),
			}
		}
	}
	return &yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!!timestamp",
		Value: t.Format(time.RFC3339),
	}
}

// forceBlockStyle walks a node tree and rewrites any flow-style sequence or
// mapping with content into block style. Empty sequences/mappings keep their
// flow style so they render as `[]` / `{}` on one line, which matches the
// vault's existing convention for empty list properties.
func forceBlockStyle(n *yaml.Node) {
	if n == nil {
		return
	}
	switch n.Kind {
	case yaml.SequenceNode, yaml.MappingNode:
		if len(n.Content) > 0 {
			n.Style = 0
		}
	}
	for _, c := range n.Content {
		forceBlockStyle(c)
	}
}

// applyWikilinkWrap recursively wraps bare strings as `[[X]]`. Non-string
// values pass through unchanged.
func applyWikilinkWrap(v any) any {
	switch vv := v.(type) {
	case string:
		return wrapWikilink(vv)
	case []any:
		out := make([]any, len(vv))
		for i, item := range vv {
			if s, ok := item.(string); ok {
				out[i] = wrapWikilink(s)
			} else {
				out[i] = item
			}
		}
		return out
	default:
		return v
	}
}
