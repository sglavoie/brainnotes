// Package brief defines Daily Brief domain contracts shared across selection,
// state persistence, service orchestration, and web/API integration.
package brief

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sglavoie/sglavoie_brainnotes/internal/textnorm"
)

// SlotKind identifies one Daily Brief recommendation slot.
type SlotKind string

const (
	SlotContinue   SlotKind = "continue"
	SlotRediscover SlotKind = "rediscover"
	SlotConnect    SlotKind = "connect"
)

// SlotOrder is the canonical v1 ordering used by storage and renderers.
var SlotOrder = []SlotKind{SlotContinue, SlotRediscover, SlotConnect}

const (
	// RecommendationCooldownDays is the v1 per-note and per-pair cooldown
	// horizon used for Rediscover and Connect exposure suppression.
	RecommendationCooldownDays = 30
	// HistoryRetentionDays is the v1 retention horizon for brief history and
	// local activity records.
	HistoryRetentionDays = 90
	// DefaultCardExcerptRunes is the v1 maximum excerpt length measured in
	// Unicode code points.
	DefaultCardExcerptRunes = 240
)

const (
	// RecommendationCooldown is the v1 exposure cooldown window.
	RecommendationCooldown = RecommendationCooldownDays * 24 * time.Hour
	// HistoryRetention is the v1 state retention window.
	HistoryRetention = HistoryRetentionDays * 24 * time.Hour
)

// LocalDate is a local calendar day in YYYY-MM-DD form.
type LocalDate string

// CardID is a stable content-derived identifier for one brief card.
type CardID string

// NoteRef identifies one vault note by normalized vault-relative path.
type NoteRef struct {
	RelPath string
	Title   string
}

// CandidateMetadata carries ranking provenance for a selected card.
type CandidateMetadata struct {
	Score        float64
	Relationship string
	Bridge       string
	Reasons      []string
}

// CorpusNote is one normalized selection candidate in the daily corpus.
type CorpusNote struct {
	Ref            NoteRef
	ModifiedAt     time.Time
	CreatedAt      time.Time
	LastActivityAt time.Time
	InboundLinks   int
	OutboundLinks  int
	BlockCount     int
	MetadataValues map[string][]string
}

// CorpusSnapshot is the deterministic in-memory brief corpus.
type CorpusSnapshot struct {
	GeneratedAt time.Time
	Notes       []CorpusNote
}

// Card is a materialized brief recommendation.
type Card struct {
	ID          CardID
	Slot        SlotKind
	Source      NoteRef
	Candidate   *NoteRef
	Explanation string
	Excerpt     string
	Metadata    CandidateMetadata
}

// UnavailableSlot explains why a slot cannot be shown for the current day.
type UnavailableSlot struct {
	Slot    SlotKind
	Message string
}

// SlotResult is either a concrete card or an unavailable explanation.
type SlotResult struct {
	Slot        SlotKind
	Card        *Card
	Unavailable *UnavailableSlot
}

// DailyBrief is the full three-slot payload for one local date.
type DailyBrief struct {
	Date  LocalDate
	Slots []SlotResult
}

// DailySelection is the persisted card set for one local date.
type DailySelection struct {
	Date  LocalDate
	Cards []Card
}

// ActivitySummary is a rolling local activity aggregate.
type ActivitySummary struct {
	WindowDays int
	VisitDays  int
	OpenCount  int
}

// RollingActivity contains the seven-day surface plus fourteen-day support data.
type RollingActivity struct {
	Last7Days  ActivitySummary
	Last14Days ActivitySummary
}

// Clock provides deterministic time for selection and state tests.
type Clock interface {
	Now() time.Time
}

// StateStore defines the persistence operations the brief service depends on.
type StateStore interface {
	ReadDailySelection(date LocalDate) (DailySelection, bool, error)
	SaveDailySelection(selection DailySelection) error
	ReplaceDailySelection(date LocalDate, cards []Card) (DailySelection, error)
	RecordVisit(date LocalDate, at time.Time) (RollingActivity, error)
	RecordOpen(cardID CardID, at time.Time) (RollingActivity, error)
	LastNoteExposure(noteRelPath string) (time.Time, bool, error)
	LastNoteOpen(noteRelPath string) (time.Time, bool, error)
	LastPairExposure(sourceRelPath, candidateRelPath string) (time.Time, bool, error)
	RollingActivity(at time.Time) (RollingActivity, error)
}

// Selector defines deterministic slot selection over a prepared corpus snapshot.
type Selector interface {
	SelectContinue(corpus CorpusSnapshot) (SlotResult, error)
	SelectRediscover(corpus CorpusSnapshot, currentContinue *Card) (SlotResult, error)
	SelectConnect(corpus CorpusSnapshot, continueSource NoteRef) (SlotResult, error)
}

// BoundCardExcerpt trims and bounds an excerpt using the v1 rune cap.
func BoundCardExcerpt(text string) string {
	return BoundExcerpt(text, DefaultCardExcerptRunes)
}

// BoundExcerpt trims, NFC-normalizes, and rune-bounds text without splitting
// Unicode code points.
func BoundExcerpt(text string, maxRunes int) string {
	text = textnorm.NFC(strings.TrimSpace(text))
	if text == "" || maxRunes <= 0 {
		return ""
	}
	if utf8.RuneCountInString(text) <= maxRunes {
		return text
	}
	if maxRunes <= 3 {
		return firstNRunes(text, maxRunes)
	}
	return firstNRunes(text, maxRunes-3) + "..."
}

func firstNRunes(text string, n int) string {
	if n <= 0 || text == "" {
		return ""
	}
	i := 0
	for idx := range text {
		if i == n {
			return text[:idx]
		}
		i++
	}
	return text
}
