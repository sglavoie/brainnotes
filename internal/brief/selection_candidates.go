package brief

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/recall"
	"github.com/sglavoie/sglavoie_brainnotes/internal/textnorm"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

func (selector *DeterministicSelector) rediscoverCandidate(note CorpusNote, now time.Time) (rediscoverCandidate, bool, error) {
	effective := note.ModifiedAt
	if note.LastActivityAt.After(effective) {
		effective = note.LastActivityAt
	}
	inCooldown := false
	if selector.state != nil {
		lastExposure, seenExposure, err := selector.state.LastNoteExposure(note.Ref.RelPath)
		if err != nil {
			return rediscoverCandidate{}, false, err
		}
		if seenExposure {
			if lastExposure.After(effective) {
				effective = lastExposure
			}
			inCooldown = activeCooldown(lastExposure, now)
		}
		lastOpen, seenOpen, err := selector.state.LastNoteOpen(note.Ref.RelPath)
		if err != nil {
			return rediscoverCandidate{}, false, err
		}
		if seenOpen && lastOpen.After(effective) {
			effective = lastOpen
		}
	}
	return rediscoverCandidate{
		note:            note,
		effectiveAt:     effective,
		connectivity:    note.InboundLinks + note.OutboundLinks,
		metadataPresent: metadataPresence(note.MetadataValues),
	}, inCooldown, nil
}

func (selector *DeterministicSelector) connectPairInCooldown(sourcePath, candidatePath string, now time.Time) (bool, error) {
	if selector.state == nil {
		return false, nil
	}
	last, ok, err := selector.state.LastPairExposure(sourcePath, candidatePath)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	return activeCooldown(last, now), nil
}

func (selector *DeterministicSelector) nowTime() time.Time {
	if selector == nil || selector.now == nil {
		return time.Now()
	}
	return selector.now()
}

func (selector *DeterministicSelector) excerptForPath(relPath string) string {
	if selector == nil {
		return ""
	}
	return selector.excerptsByID[relPath]
}

func selectionRelativePath(root, notePath string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New("brief: empty corpus root")
	}
	if notePath == "" {
		return "", errors.New("brief: empty note path")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("brief: resolve root %q: %w", root, err)
	}
	absPath, err := filepath.Abs(notePath)
	if err != nil {
		return "", fmt.Errorf("brief: resolve note %q: %w", notePath, err)
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return "", fmt.Errorf("brief: make %q relative to %q: %w", absPath, absRoot, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("brief: note %q is outside vault root %q", absPath, absRoot)
	}
	return textnorm.NFC(filepath.ToSlash(rel)), nil
}

func noteTypeValues(note *vault.Note) ([]string, error) {
	if note == nil {
		return nil, nil
	}
	front, err := note.Front()
	if err != nil {
		relPath := filepath.Base(note.Path)
		if note.Path != "" {
			relPath = note.Path
		}
		return nil, fmt.Errorf("brief: load frontmatter for %q: %w", relPath, err)
	}
	value, ok := front.Get("type")
	if !ok {
		return nil, nil
	}
	seen := make(map[string]struct{})
	for _, raw := range scalarTypeValues(value) {
		normalized := normalizeTypeValue(raw)
		if normalized == "" {
			continue
		}
		seen[normalized] = struct{}{}
	}
	if len(seen) == 0 {
		return nil, nil
	}
	values := make([]string, 0, len(seen))
	for value := range seen {
		values = append(values, value)
	}
	sort.Strings(values)
	return values, nil
}

func scalarTypeValues(value any) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		return []string{typed}
	case []string:
		return append([]string(nil), typed...)
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			values = append(values, scalarTypeValues(item)...)
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
		return []string{strconv.FormatFloat(typed, 'g', -1, 64)}
	case time.Time:
		return []string{typed.Format(time.RFC3339Nano)}
	default:
		return []string{fmt.Sprint(typed)}
	}
}

func normalizeTypeValue(value string) string {
	return strings.ToLower(textnorm.NFC(strings.TrimSpace(value)))
}

func normalizeFolderExclusions(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for i, raw := range values {
		value := textnorm.NFC(filepath.ToSlash(strings.TrimSpace(raw)))
		if value == "" {
			continue
		}
		if filepath.IsAbs(value) || path.IsAbs(value) || filepath.VolumeName(value) != "" || looksLikeWindowsDrivePath(value) {
			return nil, fmt.Errorf("brief: exclude_folders[%d]: %q must be vault-relative", i, raw)
		}
		cleaned := path.Clean(value)
		if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
			return nil, fmt.Errorf("brief: exclude_folders[%d]: %q escapes vault root", i, raw)
		}
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		normalized = append(normalized, cleaned)
	}
	sort.Strings(normalized)
	return normalized, nil
}

func normalizeTypeExclusions(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		normalized := normalizeTypeValue(value)
		if normalized == "" {
			continue
		}
		out[normalized] = struct{}{}
	}
	return out
}

func hasExcludedType(typeValues []string, excluded map[string]struct{}) bool {
	if len(typeValues) == 0 || len(excluded) == 0 {
		return false
	}
	for _, value := range typeValues {
		if _, ok := excluded[value]; ok {
			return true
		}
	}
	return false
}

func pathExcludedByPrefix(notePath string, prefixes []string) bool {
	if len(prefixes) == 0 {
		return false
	}
	for _, prefix := range prefixes {
		if notePath == prefix || strings.HasPrefix(notePath, prefix+"/") {
			return true
		}
	}
	return false
}

func metadataValuesByProperty(values []recall.MetadataValue) map[string][]string {
	if len(values) == 0 {
		return nil
	}
	grouped := make(map[string]map[string]struct{})
	for _, value := range values {
		if value.Property == "" || value.Value == "" {
			continue
		}
		if _, ok := grouped[value.Property]; !ok {
			grouped[value.Property] = make(map[string]struct{})
		}
		grouped[value.Property][value.Value] = struct{}{}
	}
	if len(grouped) == 0 {
		return nil
	}
	out := make(map[string][]string, len(grouped))
	for property, set := range grouped {
		values := make([]string, 0, len(set))
		for value := range set {
			values = append(values, value)
		}
		sort.Strings(values)
		out[property] = values
	}
	return out
}

// RepresentativeExcerpt returns the first non-empty prose, heading, or table
// block in that order, falling back to the title. The result is bounded to the
// Daily Brief card excerpt limit without splitting Unicode code points.
func RepresentativeExcerpt(document recall.Document) string {
	pick := func(kind recall.BlockKind) string {
		for _, block := range document.Blocks {
			if block.Kind != kind {
				continue
			}
			text := strings.TrimSpace(block.Text)
			if text != "" {
				return BoundCardExcerpt(text)
			}
		}
		return ""
	}
	if excerpt := pick(recall.BlockProse); excerpt != "" {
		return excerpt
	}
	if excerpt := pick(recall.BlockHeading); excerpt != "" {
		return excerpt
	}
	if excerpt := pick(recall.BlockTable); excerpt != "" {
		return excerpt
	}
	return BoundCardExcerpt(document.Title)
}

func sortRediscoverCandidates(candidates []rediscoverCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		if !candidates[i].effectiveAt.Equal(candidates[j].effectiveAt) {
			return candidates[i].effectiveAt.Before(candidates[j].effectiveAt)
		}
		if candidates[i].connectivity != candidates[j].connectivity {
			return candidates[i].connectivity > candidates[j].connectivity
		}
		if candidates[i].metadataPresent != candidates[j].metadataPresent {
			return candidates[i].metadataPresent > candidates[j].metadataPresent
		}
		return candidates[i].note.Ref.RelPath < candidates[j].note.Ref.RelPath
	})
}

func metadataPresence(values map[string][]string) int {
	if len(values) == 0 {
		return 0
	}
	count := 0
	for _, propertyValues := range values {
		if len(propertyValues) > 0 {
			count++
		}
	}
	return count
}

func activeCooldown(last, now time.Time) bool {
	if last.IsZero() {
		return false
	}
	if now.Before(last) {
		return true
	}
	return now.Sub(last) < RecommendationCooldown
}

func connectBridge(result recall.Result) string {
	switch {
	case result.BridgePath != "":
		return result.BridgePath
	case result.BridgeTitle != "":
		return result.BridgeTitle
	default:
		return ""
	}
}

func connectReasonStrings(reasons []recall.Reason) []string {
	if len(reasons) == 0 {
		return nil
	}
	out := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		if strings.TrimSpace(reason.Detail) != "" {
			out = append(out, reason.Detail)
			continue
		}
		out = append(out, string(reason.Kind))
	}
	return out
}

func unavailableSlot(slot SlotKind, message string) SlotResult {
	return SlotResult{
		Slot: slot,
		Unavailable: &UnavailableSlot{
			Slot:    slot,
			Message: message,
		},
	}
}

func cardSlot(slot SlotKind, card Card) SlotResult {
	card.Slot = slot
	return SlotResult{
		Slot: slot,
		Card: &card,
	}
}

func cloneCorpusNote(note CorpusNote) CorpusNote {
	cloned := note
	if note.MetadataValues == nil {
		return cloned
	}
	cloned.MetadataValues = make(map[string][]string, len(note.MetadataValues))
	for property, values := range note.MetadataValues {
		cloned.MetadataValues[property] = append([]string(nil), values...)
	}
	return cloned
}

func cloneRecallDocument(document recall.Document) recall.Document {
	cloned := document
	cloned.Fields = append([]recall.TextField(nil), document.Fields...)
	cloned.Blocks = append([]recall.MarkdownBlock(nil), document.Blocks...)
	cloned.Metadata = append([]recall.MetadataValue(nil), document.Metadata...)
	return cloned
}
