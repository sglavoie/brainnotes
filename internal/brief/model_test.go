package brief

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestSlotOrderV1(t *testing.T) {
	want := []SlotKind{SlotContinue, SlotRediscover, SlotConnect}
	if len(SlotOrder) != len(want) {
		t.Fatalf("SlotOrder length = %d, want %d (%v)", len(SlotOrder), len(want), SlotOrder)
	}
	for i := range want {
		if SlotOrder[i] != want[i] {
			t.Fatalf("SlotOrder[%d] = %q, want %q", i, SlotOrder[i], want[i])
		}
	}
}

func TestV1Constants(t *testing.T) {
	if RecommendationCooldownDays != 30 {
		t.Fatalf("RecommendationCooldownDays = %d, want 30", RecommendationCooldownDays)
	}
	if HistoryRetentionDays != 90 {
		t.Fatalf("HistoryRetentionDays = %d, want 90", HistoryRetentionDays)
	}
	if RecommendationCooldown != 30*24*time.Hour {
		t.Fatalf("RecommendationCooldown = %s, want %s", RecommendationCooldown, 30*24*time.Hour)
	}
	if HistoryRetention != 90*24*time.Hour {
		t.Fatalf("HistoryRetention = %s, want %s", HistoryRetention, 90*24*time.Hour)
	}
}

func TestBoundExcerpt_UnicodeSafe(t *testing.T) {
	got := BoundExcerpt("🙂🙂🙂🙂🙂", 4)
	if got != "🙂..." {
		t.Fatalf("BoundExcerpt unicode clipping = %q, want %q", got, "🙂...")
	}
	if utf8.RuneCountInString(got) != 4 {
		t.Fatalf("BoundExcerpt rune count = %d, want 4", utf8.RuneCountInString(got))
	}
}

func TestBoundExcerpt_TrimAndBoundedByDefault(t *testing.T) {
	input := "   " + strings.Repeat("a", DefaultCardExcerptRunes+20) + "   "
	got := BoundCardExcerpt(input)
	if utf8.RuneCountInString(got) != DefaultCardExcerptRunes {
		t.Fatalf("BoundCardExcerpt rune count = %d, want %d", utf8.RuneCountInString(got), DefaultCardExcerptRunes)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("BoundCardExcerpt should end with ellipsis, got %q", got)
	}
	if strings.HasPrefix(got, " ") || strings.HasSuffix(got, " ") {
		t.Fatalf("BoundCardExcerpt should trim whitespace, got %q", got)
	}
}
