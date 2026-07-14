package recall

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/textnorm"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/wikilink"
)

// ExtractDocument converts note into a deterministic recall document without
// modifying the note or any other vault state.
func ExtractDocument(root string, note *vault.Note, options ExtractOptions) (Document, error) {
	if note == nil {
		return Document{}, errors.New("recall: extract: nil note")
	}

	relPath, err := relativeNotePath(root, note.Path)
	if err != nil {
		return Document{}, err
	}
	body, err := note.Body()
	if err != nil {
		return Document{}, fmt.Errorf("recall: extract %q: %w", relPath, err)
	}
	front, err := note.Front()
	if err != nil {
		return Document{}, fmt.Errorf("recall: extract %q: %w", relPath, err)
	}

	blocks, bodyFields := extractMarkdown(body)
	title := textnorm.NFC(note.Title())
	fields := make([]TextField, 0, len(bodyFields)+1)
	if title != "" {
		fields = append(fields, TextField{
			Kind:       TextTitle,
			Text:       title,
			Weight:     TitleWeight,
			BlockIndex: -1,
		})
	}
	fields = append(fields, bodyFields...)

	return Document{
		Path:      relPath,
		Title:     title,
		Body:      body,
		Fields:    fields,
		Blocks:    blocks,
		Metadata:  extractMetadata(front, options.MetadataProperties),
		CreatedAt: extractCreatedAt(front),
	}, nil
}

// ExtractCorpus converts notes and returns documents in stable path order.
// It fails closed if any note cannot be represented, returning no partial
// corpus.
func ExtractCorpus(root string, notes []*vault.Note, options ExtractOptions) ([]Document, error) {
	documents := make([]Document, 0, len(notes))
	for _, note := range notes {
		document, err := ExtractDocument(root, note, options)
		if err != nil {
			return nil, err
		}
		documents = append(documents, document)
	}
	sort.SliceStable(documents, func(i, j int) bool {
		return documents[i].Path < documents[j].Path
	})
	return documents, nil
}

func relativeNotePath(root, notePath string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New("recall: extract: empty vault root")
	}
	if notePath == "" {
		return "", errors.New("recall: extract: empty note path")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("recall: extract: resolve root %q: %w", root, err)
	}
	absPath, err := filepath.Abs(notePath)
	if err != nil {
		return "", fmt.Errorf("recall: extract: resolve note %q: %w", notePath, err)
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return "", fmt.Errorf("recall: extract: make %q relative to %q: %w", absPath, absRoot, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("recall: extract: note %q is outside vault root %q", absPath, absRoot)
	}
	return textnorm.NFC(filepath.ToSlash(rel)), nil
}

func extractCreatedAt(front *frontmatter.Map) time.Time {
	if front == nil {
		return time.Time{}
	}
	value, ok := front.Get("created")
	if !ok {
		return time.Time{}
	}
	switch typed := value.(type) {
	case time.Time:
		return typed
	case string:
		value := strings.TrimSpace(typed)
		for _, layout := range []string{"2006-01-02", time.RFC3339Nano, time.RFC3339} {
			parsed, err := time.Parse(layout, value)
			if err == nil {
				return parsed
			}
		}
	}
	return time.Time{}
}

func extractMetadata(front *frontmatter.Map, configured []string) []MetadataValue {
	if front == nil || len(configured) == 0 {
		return nil
	}

	properties := make([]string, 0, len(configured))
	seenProperties := make(map[string]struct{}, len(configured))
	for _, property := range configured {
		property = strings.TrimSpace(property)
		if property == "" {
			continue
		}
		if _, seen := seenProperties[property]; seen {
			continue
		}
		seenProperties[property] = struct{}{}
		properties = append(properties, property)
	}
	sort.Strings(properties)

	seenPairs := make(map[MetadataValue]struct{})
	for _, property := range properties {
		value, ok := front.Get(property)
		if !ok {
			continue
		}
		normalizedProperty := normalizeMetadataText(property)
		for _, scalar := range metadataScalars(value) {
			normalizedValue := normalizeMetadataText(normalizeWikilinkValue(scalar))
			if normalizedProperty == "" || normalizedValue == "" {
				continue
			}
			seenPairs[MetadataValue{Property: normalizedProperty, Value: normalizedValue}] = struct{}{}
		}
	}

	metadata := make([]MetadataValue, 0, len(seenPairs))
	for pair := range seenPairs {
		metadata = append(metadata, pair)
	}
	sort.Slice(metadata, func(i, j int) bool {
		if metadata[i].Property != metadata[j].Property {
			return metadata[i].Property < metadata[j].Property
		}
		return metadata[i].Value < metadata[j].Value
	})
	return metadata
}

func metadataScalars(value any) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		return []string{typed}
	case []string:
		return append([]string(nil), typed...)
	case []any:
		var values []string
		for _, item := range typed {
			values = append(values, metadataScalars(item)...)
		}
		return values
	case bool:
		return []string{strconv.FormatBool(typed)}
	case int:
		return []string{strconv.Itoa(typed)}
	case int64:
		return []string{strconv.FormatInt(typed, 10)}
	case uint64:
		return []string{strconv.FormatUint(typed, 10)}
	case float64:
		formatted := strconv.FormatFloat(typed, 'g', -1, 64)
		if !strings.ContainsAny(formatted, ".eE") {
			formatted += ".0"
		}
		return []string{formatted}
	case time.Time:
		return []string{frontmatter.FormatTimestamp(typed)}
	default:
		// Mappings and unknown application types are deliberately ignored: their
		// string representations may be unstable and are not metadata values.
		return nil
	}
}

func normalizeWikilinkValue(value string) string {
	trimmed := strings.TrimSpace(value)
	links := wikilink.Parse(trimmed)
	if len(links) != 1 || links[0].Start != 0 || links[0].End != len(trimmed) {
		return trimmed
	}
	title := links[0].Title()
	extension := filepath.Ext(title)
	if strings.EqualFold(extension, ".md") && len(title) > len(extension) {
		title = strings.TrimSuffix(title, extension)
	}
	return title
}

func normalizeMetadataText(value string) string {
	return strings.ToLower(textnorm.NFC(strings.TrimSpace(value)))
}
