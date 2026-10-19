package frontmatter

import (
	"bytes"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

// Marshal serializes a Document back to bytes. Frontmatter keys are emitted
// in alphabetical order. Values for keys whose definition resolves to the
// wikilink kind are auto-wrapped as `[[Value]]` when not already wrapped.
//
// When the Front map is empty, no frontmatter block is emitted and only the
// body is returned. fmCfg may be nil; in that case no wikilink wrapping is
// applied (callers that don't need wrapping can pass nil).
func Marshal(doc Document, fmCfg *config.FrontmatterConfig) ([]byte, error) {
	var out bytes.Buffer
	body := doc.Body
	lineEnding := docFrontmatterLineEnding(doc)
	if doc.hasBOM {
		out.WriteString("\ufeff")
		if doc.bodyStartsWithFileBOM {
			body = strings.TrimPrefix(body, "\ufeff")
		}
	}
	if !doc.Front.IsEmpty() {
		out.WriteString("---" + lineEnding)
		yamlBytes, err := marshalMap(doc.Front, fmCfg)
		if err != nil {
			return nil, fmt.Errorf("frontmatter: marshal: %w", err)
		}
		yamlBytes = rewriteLineEndings(yamlBytes, lineEnding)
		out.Write(yamlBytes)
		out.WriteString("---" + lineEnding)
	} else if bodyStartsWithDelimiter(body) {
		out.WriteString("---" + lineEnding + "---" + lineEnding)
	}
	out.WriteString(body)
	return out.Bytes(), nil
}

func docFrontmatterLineEnding(doc Document) string {
	if doc.frontmatterLineEnding == "\r\n" {
		return "\r\n"
	}
	return "\n"
}

func rewriteLineEndings(data []byte, lineEnding string) []byte {
	if lineEnding != "\r\n" {
		return data
	}
	return bytes.ReplaceAll(data, []byte("\n"), []byte("\r\n"))
}

// bodyStartsWithDelimiter reports whether body begins with content that Parse
// would interpret as an opening frontmatter delimiter. When the Front map is
// empty but the body begins this way, Marshal must emit an explicit empty
// frontmatter block to prevent the body from being silently reinterpreted as
// frontmatter on the next Parse.
func bodyStartsWithDelimiter(body string) bool {
	s := body
	if len(s) >= 3 && s[0] == 0xEF && s[1] == 0xBB && s[2] == 0xBF {
		s = s[3:]
	}
	startLen, ok := openDelimLen(s)
	if !ok {
		return false
	}
	_, _, ok = findCloseDelim(s[startLen:])
	return ok
}

// marshalMap encodes the map as a sorted YAML mapping, applying wikilink
// wrapping where appropriate.
func marshalMap(m Map, fmCfg *config.FrontmatterConfig) ([]byte, error) {
	isWikilink := func(string) bool { return false }
	if fmCfg != nil {
		isWikilink = fmCfg.IsWikilink
	}

	keys := m.Keys()
	sorted := make([]string, len(keys))
	copy(sorted, keys)
	sort.Strings(sorted)

	root := &yaml.Node{Kind: yaml.MappingNode}
	for _, k := range sorted {
		v := m.vals[k]
		if isWikilink(k) {
			v = applyWikilinkWrap(v)
		}
		keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: k}
		valNode, err := encodeValueAt(v, m.meta, []string{metaMapPart(k)})
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

func encodeValueAt(v any, meta map[string]valueMeta, path []string) (*yaml.Node, error) {
	if t, ok := v.(time.Time); ok {
		valueMeta, hasMeta := meta[metaPathKey(path)]
		return encodeTime(t, valueMeta, hasMeta), nil
	}
	if f, ok := v.(float64); ok {
		return encodeFloat(f), nil
	}
	if list, ok := v.([]any); ok {
		n := &yaml.Node{Kind: yaml.SequenceNode}
		if len(list) == 0 {
			n.Style = yaml.FlowStyle
		}
		for i, item := range list {
			child, err := encodeValueAt(item, meta, appendMetaPath(path, metaIndexPart(i)))
			if err != nil {
				return nil, err
			}
			n.Content = append(n.Content, child)
		}
		return n, nil
	}
	if m, ok := v.(map[string]any); ok {
		return encodeMapAt(m, meta, path)
	}
	n := &yaml.Node{}
	if err := n.Encode(v); err != nil {
		return nil, err
	}
	forceBlockStyle(n)
	return n, nil
}

// encodeFloat preserves the YAML float tag for whole-number floats. yaml.v3's
// default encoding of float64(3) emits `3`, which then round-trips back as an
// int — silently changing the value's type and causing spurious writes for
// notes whose original frontmatter had `3.0`. Forcing the !!float tag with a
// `.0` suffix keeps the type stable across parse/marshal cycles. Non-finite
// floats (NaN/+Inf/-Inf) use YAML's canonical spellings.
func encodeFloat(f float64) *yaml.Node {
	var s string
	switch {
	case math.IsNaN(f):
		s = ".nan"
	case math.IsInf(f, 1):
		s = ".inf"
	case math.IsInf(f, -1):
		s = "-.inf"
	default:
		s = strconv.FormatFloat(f, 'g', -1, 64)
		// Ensure the scalar reads as a float rather than an int on re-parse.
		// strconv.FormatFloat omits the decimal for whole numbers and may use
		// scientific notation; in either of those cases the YAML parser would
		// decode the value as int unless we also pin the !!float tag (which we
		// do below). The textual suffix is purely cosmetic for the whole-number
		// case so the file matches the typical vault convention of `3.0`.
		if !hasFloatMarker(s) {
			s += ".0"
		}
	}
	return &yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!!float",
		Value: s,
	}
}

// hasFloatMarker reports whether s contains a character that the YAML 1.2 core
// schema treats as a float-only token (decimal point or exponent).
func hasFloatMarker(s string) bool {
	for _, r := range s {
		if r == '.' || r == 'e' || r == 'E' {
			return true
		}
	}
	return false
}

func encodeMapAt(m map[string]any, meta map[string]valueMeta, path []string) (*yaml.Node, error) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	n := &yaml.Node{Kind: yaml.MappingNode}
	if len(keys) == 0 {
		n.Style = yaml.FlowStyle
	}
	for _, k := range keys {
		keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: k}
		valNode, err := encodeValueAt(m[k], meta, appendMetaPath(path, metaMapPart(k)))
		if err != nil {
			return nil, fmt.Errorf("encode nested %q: %w", k, err)
		}
		n.Content = append(n.Content, keyNode, valNode)
	}
	return n, nil
}

// encodeTime emits a date-only scalar when the time has no time-of-day
// component and is in UTC, unless a recorded spelling — from a parsed datetime
// scalar or from Map.SetTimeSpelling — says the value is a timestamp;
// otherwise it emits a full RFC3339 timestamp.
// Sub-second precision is preserved via RFC3339Nano so that timestamps
// like `2025-12-13T10:30:00.5Z` round-trip without losing fractional
// seconds.
func encodeTime(t time.Time, meta valueMeta, hasMeta bool) *yaml.Node {
	if (!hasMeta || meta.timestampDateOnly) && isMidnightUTC(t) {
		return &yaml.Node{
			Kind:  yaml.ScalarNode,
			Tag:   "!!timestamp",
			Value: t.Format("2006-01-02"),
		}
	}
	return &yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!!timestamp",
		Value: t.Format(time.RFC3339Nano),
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

// applyWikilinkWrap wraps bare scalar strings and string items in a list as
// `[[X]]`. Non-string values pass through unchanged.
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
	case []string:
		out := make([]string, len(vv))
		for i, item := range vv {
			out[i] = wrapWikilink(item)
		}
		return out
	default:
		return v
	}
}
