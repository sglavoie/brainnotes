package frontmatter

import (
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Parse extracts frontmatter and body from a markdown document. A file that
// does not begin with `---\n` (or `---\r\n`) is treated as body-only with an
// empty Front map. A file that opens a frontmatter block but never closes it
// is also treated as body-only — the alternative (an error) would prevent
// editing notes that happen to begin with a literal `---` line.
//
// Windows line endings (`\r\n`) are accepted on the delimiter lines; the body
// is preserved verbatim.
func Parse(data []byte) (Document, error) {
	raw := string(data)
	hasBOM := strings.HasPrefix(raw, "\ufeff")
	s := raw
	if hasBOM {
		s = strings.TrimPrefix(s, "\ufeff")
	}

	startLen, ok := openDelimLen(s)
	if !ok {
		return Document{Front: NewMap(), Body: raw}, nil
	}

	rest := s[startLen:]
	closeOff, bodyOff, ok := findCloseDelim(rest)
	if !ok {
		return Document{Front: NewMap(), Body: raw}, nil
	}

	yamlText := rest[:closeOff]
	body := rest[bodyOff:]

	m, err := parseYAML([]byte(yamlText))
	if err != nil {
		return Document{}, fmt.Errorf("frontmatter: parse yaml: %w", err)
	}
	return Document{Front: m, Body: body, hasBOM: hasBOM}, nil
}

// openDelimLen reports the byte length of the opening `---` line if s starts
// with one. The line may have trailing spaces or tabs before the newline.
func openDelimLen(s string) (int, bool) {
	if !strings.HasPrefix(s, "---") {
		return 0, false
	}
	nl := strings.IndexByte(s, '\n')
	if nl < 0 {
		return 0, false
	}
	line := s[:nl]
	if isDelimLine(line) {
		return nl + 1, true
	}
	return 0, false
}

// isDelimLine reports whether line is a `---` frontmatter delimiter, tolerating
// trailing whitespace and a trailing carriage return (for CRLF files).
func isDelimLine(line string) bool {
	line = strings.TrimRight(line, "\r")
	line = strings.TrimRight(line, " \t")
	return line == "---"
}

// findCloseDelim scans s line-by-line for a closing `---` (optionally with a
// trailing `\r`). It returns the byte offset at which the closing line starts
// and the byte offset at which the body begins (just past the closing line's
// newline). A trailing `---` with no newline (end of file) is also accepted,
// in which case bodyOff == len(s).
func findCloseDelim(s string) (closeOff, bodyOff int, ok bool) {
	i := 0
	for i < len(s) {
		// Locate end of the current line.
		eol := strings.IndexByte(s[i:], '\n')
		var line string
		var lineEnd int
		if eol == -1 {
			line = s[i:]
			lineEnd = len(s)
		} else {
			line = s[i : i+eol]
			lineEnd = i + eol + 1
		}
		if isDelimLine(line) {
			return i, lineEnd, true
		}
		if eol == -1 {
			break
		}
		i = lineEnd
	}
	return 0, 0, false
}

// parseYAML decodes a YAML mapping while preserving the source key order.
func parseYAML(data []byte) (Map, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return NewMap(), nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return Map{}, err
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return NewMap(), nil
	}
	root := doc.Content[0]
	switch root.Kind {
	case yaml.MappingNode:
		// fall through
	case yaml.ScalarNode:
		// A document that is just `null` is treated as empty.
		if root.Tag == "!!null" {
			return NewMap(), nil
		}
		return Map{}, fmt.Errorf("top-level must be a mapping, got scalar")
	default:
		return Map{}, fmt.Errorf("top-level must be a mapping")
	}

	m := NewMap()
	for i := 0; i+1 < len(root.Content); i += 2 {
		keyNode := root.Content[i]
		valNode := root.Content[i+1]
		var key string
		if err := keyNode.Decode(&key); err != nil {
			return Map{}, fmt.Errorf("decode key: %w", err)
		}
		path := []string{metaMapPart(key)}
		clearMetaPrefix(m.meta, path)
		val, err := decodeNodeAt(valNode, path, m.meta)
		if err != nil {
			return Map{}, fmt.Errorf("decode value for %q: %w", key, err)
		}
		m.setParsed(key, val)
	}
	return m, nil
}

func decodeNodeAt(n *yaml.Node, path []string, meta map[string]valueMeta) (any, error) {
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Tag == "!!null" || (n.Tag == "" && n.Value == "") {
			clearScalarMeta(path, meta, n)
			return nil, nil
		}
		var v any
		if err := n.Decode(&v); err != nil {
			return nil, err
		}
		setScalarMeta(path, meta, n)
		return v, nil
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for i, c := range n.Content {
			item, err := decodeNodeAt(c, appendMetaPath(path, metaIndexPart(i)), meta)
			if err != nil {
				return nil, err
			}
			out = append(out, item)
		}
		return out, nil
	case yaml.MappingNode:
		v := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			var key string
			if err := n.Content[i].Decode(&key); err != nil {
				return nil, fmt.Errorf("decode nested key: %w", err)
			}
			childPath := appendMetaPath(path, metaMapPart(key))
			clearMetaPrefix(meta, childPath)
			val, err := decodeNodeAt(n.Content[i+1], childPath, meta)
			if err != nil {
				return nil, fmt.Errorf("decode nested value for %q: %w", key, err)
			}
			v[key] = val
		}
		return v, nil
	case yaml.AliasNode:
		if n.Alias == nil {
			return nil, fmt.Errorf("dangling alias")
		}
		return decodeNodeAt(n.Alias, path, meta)
	}
	var v any
	if err := n.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

func clearScalarMeta(path []string, meta map[string]valueMeta, n *yaml.Node) {
	if len(path) == 0 || meta == nil {
		return
	}
	if n.Tag != "!!timestamp" {
		delete(meta, metaPathKey(path))
	}
}

func setScalarMeta(path []string, meta map[string]valueMeta, n *yaml.Node) {
	if len(path) == 0 || meta == nil {
		return
	}
	key := metaPathKey(path)
	if n.Tag != "!!timestamp" {
		delete(meta, key)
		return
	}
	meta[key] = valueMeta{timestampDateOnly: isDateOnlyTimestampLiteral(n.Value)}
}

func isDateOnlyTimestampLiteral(s string) bool {
	_, err := time.Parse("2006-01-02", strings.TrimSpace(s))
	return err == nil
}
