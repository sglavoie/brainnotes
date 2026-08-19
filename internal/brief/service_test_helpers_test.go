package brief

import (
	"reflect"
	"testing"
)

type staticSelector struct{}

func (staticSelector) SelectContinue(corpus CorpusSnapshot) (SlotResult, error) {
	if len(corpus.Notes) == 0 {
		return unavailableSlot(SlotContinue, "No substantive notes are available."), nil
	}
	return cardSlot(SlotContinue, Card{
		Slot:        SlotContinue,
		Source:      corpus.Notes[0].Ref,
		Explanation: "Static selector fallback.",
		Excerpt:     "fallback",
	}), nil
}

func (staticSelector) SelectRediscover(CorpusSnapshot, *Card) (SlotResult, error) {
	return unavailableSlot(SlotRediscover, "No rediscover candidate is available."), nil
}

func (staticSelector) SelectConnect(CorpusSnapshot, NoteRef) (SlotResult, error) {
	return unavailableSlot(SlotConnect, "No connect candidate is available."), nil
}

func loadBriefCorpus(t *testing.T, root string) SelectionCorpus {
	t.Helper()
	notes, graph := loadSelectionFixture(t, root)
	corpus, err := BuildSelectionCorpus(root, notes, graph, SelectionCorpusOptions{})
	if err != nil {
		t.Fatalf("BuildSelectionCorpus: %v", err)
	}
	return corpus
}

func slotCardIDs(brief DailyBrief) map[SlotKind]CardID {
	out := make(map[SlotKind]CardID, len(brief.Slots))
	for _, slot := range brief.Slots {
		if slot.Card == nil {
			continue
		}
		out[slot.Slot] = slot.Card.ID
	}
	return out
}

func cardForSlot(t *testing.T, brief DailyBrief, slot SlotKind) Card {
	t.Helper()
	for _, result := range brief.Slots {
		if result.Slot != slot {
			continue
		}
		if result.Card == nil {
			t.Fatalf("slot %q unavailable: %#v", slot, result.Unavailable)
		}
		return cloneCard(*result.Card)
	}
	t.Fatalf("slot %q missing", slot)
	return Card{}
}

func assertAffectedCardsMatchDaily(t *testing.T, result AnotherResult) {
	t.Helper()
	for _, affected := range result.Affected {
		if affected.Card == nil {
			continue
		}
		if affected.Card.ID == "" {
			t.Fatalf("affected slot %q card ID is empty: %#v", affected.Slot, affected.Card)
		}
		dailyCard := cardForSlot(t, result.Daily.Brief, affected.Slot)
		if !reflect.DeepEqual(*affected.Card, dailyCard) {
			t.Fatalf("affected slot %q card does not match daily card:\naffected=%#v\ndaily=%#v", affected.Slot, *affected.Card, dailyCard)
		}
	}
}
