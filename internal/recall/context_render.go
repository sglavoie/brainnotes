package recall

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

func contextBundleWithinLimit(bundle ContextBundle, maxBytes int) (bool, error) {
	markdown, err := renderContextMarkdown(bundle)
	if err != nil {
		return false, err
	}
	if len(markdown) > maxBytes {
		return false, nil
	}
	jsonData, err := renderContextJSON(bundle)
	if err != nil {
		return false, err
	}
	return len(jsonData) <= maxBytes, nil
}

func renderContextMarkdown(bundle ContextBundle) ([]byte, error) {
	return renderContextFixedPoint(bundle, func(current ContextBundle) ([]byte, error) {
		var output strings.Builder
		output.WriteString("# Research Context\n\n")
		output.WriteString("## Purpose\n\n")
		output.WriteString("- Name: ")
		output.WriteString(strconv.Quote(current.Purpose.Name))
		output.WriteString("\n\n")
		if current.Purpose.Instructions == "" {
			output.WriteString("(none)\n\n")
		} else {
			output.WriteString(current.Purpose.Instructions)
			if !strings.HasSuffix(current.Purpose.Instructions, "\n") {
				output.WriteByte('\n')
			}
			output.WriteByte('\n')
		}

		output.WriteString("## Manifest\n\n")
		output.WriteString("- Source: ")
		output.WriteString(strconv.Quote(current.Manifest.SourcePath))
		output.WriteByte('\n')
		output.WriteString("- Excerpts:\n")
		if len(current.Manifest.Excerpts) == 0 {
			output.WriteString("  - (none)\n")
		} else {
			for _, excerpt := range current.Manifest.Excerpts {
				fmt.Fprintf(
					&output,
					"  - %s (lines %d-%d)\n",
					strconv.Quote(excerpt.Path),
					excerpt.StartLine,
					excerpt.EndLine,
				)
			}
		}
		fmt.Fprintf(&output, "- Maximum bytes: %d\n", current.Manifest.MaxBytes)
		fmt.Fprintf(&output, "- Byte count: %d\n\n", current.Manifest.ByteCount)

		output.WriteString("## Source\n\n")
		writeContextIdentity(&output, current.Source.Identity)
		writeContextMetadata(&output, current.Source.Metadata)
		output.WriteString("### Complete body\n\n")
		output.WriteString("<!-- BEGIN SOURCE BODY -->\n")
		output.WriteString(current.Source.Body)
		if !strings.HasSuffix(current.Source.Body, "\n") {
			output.WriteByte('\n')
		}
		output.WriteString("<!-- END SOURCE BODY -->\n\n")

		output.WriteString("## Related Notes\n\n")
		if len(current.Related) == 0 {
			output.WriteString("(none)\n")
		}
		for _, related := range current.Related {
			fmt.Fprintf(&output, "### %d. %s\n\n", related.Rank, strconv.Quote(related.Identity.Title))
			writeContextIdentity(&output, related.Identity)
			fmt.Fprintf(&output, "- Score: %s\n", strconv.FormatFloat(related.Score, 'g', -1, 64))
			relationship := string(related.Relationship)
			if relationship == "" {
				relationship = "none"
			}
			output.WriteString("- Relationship: ")
			output.WriteString(strconv.Quote(relationship))
			output.WriteByte('\n')
			if related.Bridge != nil {
				output.WriteString("- Bridge path: ")
				output.WriteString(strconv.Quote(related.Bridge.Path))
				output.WriteByte('\n')
				output.WriteString("- Bridge title: ")
				output.WriteString(strconv.Quote(related.Bridge.Title))
				output.WriteByte('\n')
			}
			writeContextMetadata(&output, related.Metadata)
			writeContextReasons(&output, related.Reasons)
			if len(related.Excerpts) == 0 {
				output.WriteString("#### Excerpts\n\n(none)\n\n")
				continue
			}
			for excerptIndex, excerpt := range related.Excerpts {
				fmt.Fprintf(&output, "#### Excerpt %d\n\n", excerptIndex+1)
				output.WriteString("- Path: ")
				output.WriteString(strconv.Quote(excerpt.Path))
				output.WriteByte('\n')
				fmt.Fprintf(&output, "- Lines: %d-%d\n", excerpt.StartLine, excerpt.EndLine)
				fmt.Fprintf(&output, "- Truncated: %t\n", excerpt.Truncated)
				output.WriteString("- Distinctive terms: ")
				if len(excerpt.DistinctiveTerms) == 0 {
					output.WriteString("(none)\n")
				} else {
					quoted := make([]string, len(excerpt.DistinctiveTerms))
					for index, term := range excerpt.DistinctiveTerms {
						quoted[index] = strconv.Quote(term)
					}
					output.WriteString(strings.Join(quoted, ", "))
					output.WriteByte('\n')
				}
				writeContextReasons(&output, excerpt.Reasons)
				output.WriteString("\n<!-- BEGIN RELATED EXCERPT -->\n")
				output.WriteString(excerpt.Markdown)
				if !strings.HasSuffix(excerpt.Markdown, "\n") {
					output.WriteByte('\n')
				}
				output.WriteString("<!-- END RELATED EXCERPT -->\n\n")
			}
		}
		return []byte(output.String()), nil
	})
}

func writeContextIdentity(output *strings.Builder, identity ContextNoteIdentity) {
	output.WriteString("- Path: ")
	output.WriteString(strconv.Quote(identity.Path))
	output.WriteByte('\n')
	output.WriteString("- Title: ")
	output.WriteString(strconv.Quote(identity.Title))
	output.WriteByte('\n')
}

func writeContextMetadata(output *strings.Builder, metadata []ContextMetadataValue) {
	output.WriteString("- Metadata:")
	if len(metadata) == 0 {
		output.WriteString(" (none)\n")
		return
	}
	output.WriteByte('\n')
	for _, value := range metadata {
		output.WriteString("  - ")
		output.WriteString(strconv.Quote(value.Property))
		output.WriteString(" = ")
		output.WriteString(strconv.Quote(value.Value))
		output.WriteByte('\n')
	}
}

func writeContextReasons(output *strings.Builder, reasons []ContextReason) {
	output.WriteString("- Reasons:")
	if len(reasons) == 0 {
		output.WriteString(" (none)\n")
		return
	}
	output.WriteByte('\n')
	for _, reason := range reasons {
		fmt.Fprintf(
			output,
			"  - %s (%s): %s\n",
			reason.Kind,
			strconv.FormatFloat(reason.Contribution, 'g', -1, 64),
			strconv.Quote(reason.Detail),
		)
	}
}

func renderContextJSON(bundle ContextBundle) ([]byte, error) {
	return renderContextFixedPoint(bundle, func(current ContextBundle) ([]byte, error) {
		var output bytes.Buffer
		encoder := json.NewEncoder(&output)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(current); err != nil {
			return nil, fmt.Errorf("recall: render context JSON: %w", err)
		}
		return output.Bytes(), nil
	})
}

func renderContextFixedPoint(
	bundle ContextBundle,
	render func(ContextBundle) ([]byte, error),
) ([]byte, error) {
	byteCount := 0
	for range 16 {
		current := bundle
		current.Manifest = buildContextManifest(current, byteCount)
		data, err := render(current)
		if err != nil {
			return nil, err
		}
		if len(data) == byteCount {
			return data, nil
		}
		byteCount = len(data)
	}
	return nil, errors.New("recall: context byte count did not stabilize")
}
