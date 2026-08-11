package brief

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/textnorm"
)

// CardIDFor returns the deterministic identifier for one daily slot card.
func CardIDFor(date LocalDate, slot SlotKind, sourceRelPath, candidateRelPath string) (CardID, error) {
	date, err := normalizeLocalDate(date)
	if err != nil {
		return "", err
	}
	if !validSlotKind(slot) {
		return "", fmt.Errorf("brief: unknown slot %q", slot)
	}
	sourceRelPath, err = normalizeBriefPath(sourceRelPath)
	if err != nil {
		return "", fmt.Errorf("brief: source path: %w", err)
	}

	candidateRelPath = textnorm.NFC(filepath.ToSlash(candidateRelPath))
	switch slot {
	case SlotConnect:
		candidateRelPath, err = normalizeBriefPath(candidateRelPath)
		if err != nil {
			return "", fmt.Errorf("brief: candidate path: %w", err)
		}
		if sourceRelPath == candidateRelPath {
			return "", errors.New("brief: connect source and candidate paths must differ")
		}
	default:
		if candidateRelPath != "" {
			return "", fmt.Errorf("brief: slot %q must not include a candidate path", slot)
		}
	}

	sum := sha256.Sum256([]byte(string(date) + "\x00" + string(slot) + "\x00" + sourceRelPath + "\x00" + candidateRelPath))
	return CardID(hex.EncodeToString(sum[:])), nil
}

func normalizeDailySelection(selection DailySelection) (DailySelection, error) {
	date, err := normalizeLocalDate(selection.Date)
	if err != nil {
		return DailySelection{}, err
	}
	cards, err := normalizeCardsForDate(date, selection.Cards)
	if err != nil {
		return DailySelection{}, err
	}
	return DailySelection{
		Date:  date,
		Cards: cards,
	}, nil
}

func normalizeCardsForDate(date LocalDate, cards []Card) ([]Card, error) {
	normalized := make(map[SlotKind]Card, len(cards))
	for _, card := range cards {
		card, err := normalizeCardForDate(date, card)
		if err != nil {
			return nil, err
		}
		if _, exists := normalized[card.Slot]; exists {
			return nil, fmt.Errorf("brief: duplicate slot %q in one selection", card.Slot)
		}
		normalized[card.Slot] = card
	}
	out := make([]Card, 0, len(normalized))
	for _, slot := range SlotOrder {
		card, ok := normalized[slot]
		if ok {
			out = append(out, card)
		}
	}
	if len(out) != len(normalized) {
		return nil, errors.New("brief: selection contains unknown slots")
	}
	return out, nil
}

func normalizeCardForDate(date LocalDate, card Card) (Card, error) {
	if !validSlotKind(card.Slot) {
		return Card{}, fmt.Errorf("brief: unknown slot %q", card.Slot)
	}
	sourceRelPath, err := normalizeBriefPath(card.Source.RelPath)
	if err != nil {
		return Card{}, fmt.Errorf("brief: source path: %w", err)
	}
	card.Source.RelPath = sourceRelPath

	candidateRelPath := ""
	switch card.Slot {
	case SlotConnect:
		if card.Candidate == nil {
			return Card{}, errors.New("brief: connect card requires a candidate")
		}
		candidateRelPath, err = normalizeBriefPath(card.Candidate.RelPath)
		if err != nil {
			return Card{}, fmt.Errorf("brief: candidate path: %w", err)
		}
		if candidateRelPath == sourceRelPath {
			return Card{}, errors.New("brief: connect source and candidate paths must differ")
		}
		card.Candidate.RelPath = candidateRelPath
	default:
		if card.Candidate != nil {
			return Card{}, fmt.Errorf("brief: slot %q must not include a candidate", card.Slot)
		}
	}
	expectedID, err := CardIDFor(date, card.Slot, sourceRelPath, candidateRelPath)
	if err != nil {
		return Card{}, err
	}
	if card.ID == "" {
		return Card{}, errors.New("brief: missing card ID")
	}
	if card.ID != expectedID {
		return Card{}, fmt.Errorf(
			"brief: card %q ID %q does not match expected %q",
			card.Slot,
			card.ID,
			expectedID,
		)
	}
	if err := validateCardIDFormat(card.ID); err != nil {
		return Card{}, err
	}
	card.Metadata.Reasons = append([]string(nil), card.Metadata.Reasons...)
	return card, nil
}

func validSlotKind(slot SlotKind) bool {
	switch slot {
	case SlotContinue, SlotRediscover, SlotConnect:
		return true
	default:
		return false
	}
}

func normalizeLocalDate(value LocalDate) (LocalDate, error) {
	text := strings.TrimSpace(string(value))
	if text == "" {
		return "", errors.New("brief: empty local date")
	}
	parsed, err := time.Parse("2006-01-02", text)
	if err != nil {
		return "", fmt.Errorf("brief: invalid local date %q: %w", value, err)
	}
	normalized := parsed.Format("2006-01-02")
	if normalized != text {
		return "", fmt.Errorf("brief: invalid local date %q", value)
	}
	return LocalDate(normalized), nil
}

func normalizeBriefPair(sourceRelPath, candidateRelPath string) (string, string, string, error) {
	sourceRelPath, err := normalizeBriefPath(sourceRelPath)
	if err != nil {
		return "", "", "", fmt.Errorf("brief: source path: %w", err)
	}
	candidateRelPath, err = normalizeBriefPath(candidateRelPath)
	if err != nil {
		return "", "", "", fmt.Errorf("brief: candidate path: %w", err)
	}
	if sourceRelPath == candidateRelPath {
		return "", "", "", errors.New("brief: source and candidate paths must differ")
	}
	sum := sha256.Sum256([]byte(sourceRelPath + "\x00" + candidateRelPath))
	return sourceRelPath, candidateRelPath, hex.EncodeToString(sum[:]), nil
}

func normalizeBriefPath(value string) (string, error) {
	if value == "" {
		return "", errors.New("empty vault-relative path")
	}
	if strings.ContainsRune(value, '\x00') {
		return "", errors.New("vault-relative path contains NUL")
	}
	if strings.Contains(value, `\`) {
		return "", fmt.Errorf("vault-relative path %q must use forward slashes", value)
	}
	value = textnorm.NFC(filepath.ToSlash(value))
	if filepath.IsAbs(value) || path.IsAbs(value) || filepath.VolumeName(value) != "" || looksLikeWindowsDrivePath(value) {
		return "", fmt.Errorf("path %q must be vault-relative", value)
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("path %q escapes the vault root", value)
	}
	if cleaned != value {
		return "", fmt.Errorf("path %q is not a normalized vault-relative path", value)
	}
	return value, nil
}

func looksLikeWindowsDrivePath(value string) bool {
	if len(value) < 2 || value[1] != ':' {
		return false
	}
	first := value[0]
	return (first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z')
}

func validateCardIDFormat(id CardID) error {
	text := string(id)
	if len(text) != sha256.Size*2 {
		return fmt.Errorf("brief: malformed card ID %q", id)
	}
	for _, r := range text {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		default:
			return fmt.Errorf("brief: malformed card ID %q", id)
		}
	}
	return nil
}

func validateBriefState(state briefStateFile) error {
	for dateKey, selection := range state.DailySelections {
		date, err := normalizeLocalDate(LocalDate(dateKey))
		if err != nil {
			return fmt.Errorf("%w: selection key %q: %v", ErrCorruptState, dateKey, err)
		}
		if selection.Date == "" {
			selection.Date = date
		}
		if selection.Date != date {
			return fmt.Errorf(
				"%w: selection %q date payload %q does not match its key",
				ErrCorruptState,
				date,
				selection.Date,
			)
		}
		cards, err := normalizeCardsForDate(date, selection.Cards)
		if err != nil {
			return fmt.Errorf("%w: selection %q: %v", ErrCorruptState, date, err)
		}
		selection.Cards = cards
	}
	for relPath, exposedAt := range state.NoteExposures {
		normalized, err := normalizeBriefPath(relPath)
		if err != nil {
			return fmt.Errorf("%w: note exposure %q: %v", ErrCorruptState, relPath, err)
		}
		if normalized != relPath {
			return fmt.Errorf("%w: note exposure %q is not normalized", ErrCorruptState, relPath)
		}
		if exposedAt.IsZero() {
			return fmt.Errorf("%w: note exposure %q has no timestamp", ErrCorruptState, relPath)
		}
	}
	for pairID, record := range state.PairExposures {
		normalizedSource, normalizedCandidate, expectedID, err := normalizeBriefPair(record.SourceRelPath, record.CandidateRelPath)
		if err != nil {
			return fmt.Errorf("%w: pair exposure %q: %v", ErrCorruptState, pairID, err)
		}
		if pairID != expectedID {
			return fmt.Errorf("%w: pair exposure %q does not match source/candidate", ErrCorruptState, pairID)
		}
		if normalizedSource != record.SourceRelPath || normalizedCandidate != record.CandidateRelPath {
			return fmt.Errorf("%w: pair exposure %q contains non-normalized paths", ErrCorruptState, pairID)
		}
		if record.ExposedAt.IsZero() {
			return fmt.Errorf("%w: pair exposure %q has no timestamp", ErrCorruptState, pairID)
		}
	}
	for visitDate, visitedAt := range state.Visits {
		normalized, err := normalizeLocalDate(LocalDate(visitDate))
		if err != nil {
			return fmt.Errorf("%w: visit date %q: %v", ErrCorruptState, visitDate, err)
		}
		if LocalDate(visitDate) != normalized {
			return fmt.Errorf("%w: visit date %q is not normalized", ErrCorruptState, visitDate)
		}
		if visitedAt.IsZero() {
			return fmt.Errorf("%w: visit date %q has no timestamp", ErrCorruptState, visitDate)
		}
	}
	for i, open := range state.Opens {
		if err := validateCardIDFormat(open.CardID); err != nil {
			return fmt.Errorf("%w: open[%d]: %v", ErrCorruptState, i, err)
		}
		if open.OpenedAt.IsZero() {
			return fmt.Errorf("%w: open[%d] has no timestamp", ErrCorruptState, i)
		}
	}
	return nil
}
