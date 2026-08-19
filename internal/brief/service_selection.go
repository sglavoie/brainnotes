package brief

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
)

func (service *Service) repairSelection(date LocalDate, selection DailySelection, corpus CorpusSnapshot) (DailySelection, map[SlotKind]UnavailableSlot, error) {
	normalizedSelection, err := normalizeDailySelection(selection)
	if err != nil {
		return DailySelection{}, nil, err
	}
	paths, err := corpusPathSet(corpus)
	if err != nil {
		return DailySelection{}, nil, err
	}
	bySlot := cardBySlot(normalizedSelection.Cards)
	unavailable := make(map[SlotKind]UnavailableSlot)

	continueCard, hasContinue := bySlot[SlotContinue]
	continueValid := hasContinue && pathInCorpus(paths, continueCard.Source.RelPath)
	rediscoverCard, hasRediscover := bySlot[SlotRediscover]
	rediscoverValid := hasRediscover && pathInCorpus(paths, rediscoverCard.Source.RelPath)
	connectCard, hasConnect := bySlot[SlotConnect]
	connectValid := hasConnect &&
		connectCard.Candidate != nil &&
		pathInCorpus(paths, connectCard.Source.RelPath) &&
		pathInCorpus(paths, connectCard.Candidate.RelPath)
	if connectValid && continueValid && connectCard.Source.RelPath != continueCard.Source.RelPath {
		connectValid = false
	}

	needsContinue := hasContinue && !continueValid
	needsRediscover := hasRediscover && !rediscoverValid
	needsConnect := hasConnect && !connectValid
	if needsContinue && hasConnect {
		needsConnect = true
	}

	if !needsContinue && !needsRediscover && !needsConnect {
		return normalizedSelection, unavailable, nil
	}

	replacements := make(map[SlotKind]Card)
	removed := make(map[SlotKind]struct{})

	var currentContinue *Card
	if continueValid {
		cloned := cloneCard(continueCard)
		currentContinue = &cloned
	}

	if needsContinue {
		result, err := service.selectContinue(corpus, continueCard.Source.RelPath)
		if err != nil {
			return DailySelection{}, nil, err
		}
		next, ok, err := materializeSlotCard(date, result)
		if err != nil {
			return DailySelection{}, nil, err
		}
		if ok {
			replacements[SlotContinue] = next
			cloned := cloneCard(next)
			currentContinue = &cloned
		} else {
			removed[SlotContinue] = struct{}{}
			currentContinue = nil
			unavailable[SlotContinue] = unavailableFromResult(result, defaultUnavailableMessage(SlotContinue))
		}
	}

	if needsRediscover {
		result, err := service.selector.SelectRediscover(corpus, currentContinue)
		if err != nil {
			return DailySelection{}, nil, err
		}
		next, ok, err := materializeSlotCard(date, result)
		if err != nil {
			return DailySelection{}, nil, err
		}
		if ok {
			replacements[SlotRediscover] = next
		} else {
			removed[SlotRediscover] = struct{}{}
			unavailable[SlotRediscover] = unavailableFromResult(result, defaultUnavailableMessage(SlotRediscover))
		}
	}

	if needsConnect {
		result, err := service.selectConnect(corpus, currentContinue)
		if err != nil {
			return DailySelection{}, nil, err
		}
		next, ok, err := materializeSlotCard(date, result)
		if err != nil {
			return DailySelection{}, nil, err
		}
		if ok {
			replacements[SlotConnect] = next
		} else {
			removed[SlotConnect] = struct{}{}
			unavailable[SlotConnect] = unavailableFromResult(result, defaultConnectUnavailableMessage(currentContinue != nil))
		}
	}

	updated, err := service.persistMutation(date, normalizedSelection, replacements, removed)
	if err != nil {
		return DailySelection{}, nil, err
	}
	return updated, unavailable, nil
}

func (service *Service) persistMutation(
	date LocalDate,
	selection DailySelection,
	replacements map[SlotKind]Card,
	removed map[SlotKind]struct{},
) (DailySelection, error) {
	if len(replacements) == 0 && len(removed) == 0 {
		return selection, nil
	}
	if len(removed) == 0 {
		replaceCards := make([]Card, 0, len(replacements))
		for _, slot := range SlotOrder {
			card, ok := replacements[slot]
			if !ok {
				continue
			}
			replaceCards = append(replaceCards, card)
		}
		if len(replaceCards) == 0 {
			return selection, nil
		}
		return service.state.ReplaceDailySelection(date, replaceCards)
	}

	bySlot := cardBySlot(selection.Cards)
	for slot := range removed {
		delete(bySlot, slot)
	}
	for slot, card := range replacements {
		bySlot[slot] = card
	}
	updatedCards := make([]Card, 0, len(SlotOrder))
	for _, slot := range SlotOrder {
		card, ok := bySlot[slot]
		if !ok {
			continue
		}
		updatedCards = append(updatedCards, card)
	}
	updated, err := normalizeDailySelection(DailySelection{
		Date:  date,
		Cards: updatedCards,
	})
	if err != nil {
		return DailySelection{}, err
	}
	if reflect.DeepEqual(updated.Cards, selection.Cards) {
		return updated, nil
	}
	if err := service.state.SaveDailySelection(updated); err != nil {
		return DailySelection{}, err
	}
	return updated, nil
}

func (service *Service) selectContinue(corpus CorpusSnapshot, excludePath string) (SlotResult, error) {
	if strings.TrimSpace(excludePath) == "" {
		return service.selector.SelectContinue(corpus)
	}
	normalizedExclude, err := normalizeBriefPath(excludePath)
	if err != nil {
		return SlotResult{}, err
	}
	filtered := CorpusSnapshot{
		GeneratedAt: corpus.GeneratedAt,
		Notes:       make([]CorpusNote, 0, len(corpus.Notes)),
	}
	for _, note := range corpus.Notes {
		if note.Ref.RelPath == normalizedExclude {
			continue
		}
		filtered.Notes = append(filtered.Notes, cloneCorpusNote(note))
	}
	return service.selector.SelectContinue(filtered)
}

func (service *Service) selectConnect(corpus CorpusSnapshot, continueCard *Card) (SlotResult, error) {
	if continueCard == nil {
		return unavailableSlot(SlotConnect, defaultConnectUnavailableMessage(false)), nil
	}
	return service.selector.SelectConnect(corpus, continueCard.Source)
}

func (service *Service) nowAndDate() (time.Time, LocalDate, error) {
	if service == nil {
		return time.Time{}, "", errors.New("brief: nil service")
	}
	if service.now == nil {
		return time.Time{}, "", errors.New("brief: nil service clock")
	}
	now := service.now()
	if now.IsZero() {
		return time.Time{}, "", errors.New("brief: zero service clock time")
	}
	return now, LocalDate(now.Format("2006-01-02")), nil
}

func buildDailyBrief(
	date LocalDate,
	selection DailySelection,
	unavailable map[SlotKind]UnavailableSlot,
) DailyBrief {
	if unavailable == nil {
		unavailable = make(map[SlotKind]UnavailableSlot)
	}
	bySlot := cardBySlot(selection.Cards)
	_, hasContinue := bySlot[SlotContinue]
	slots := make([]SlotResult, 0, len(SlotOrder))
	for _, slot := range SlotOrder {
		card, ok := bySlot[slot]
		if ok {
			slots = append(slots, cardSlot(slot, cloneCard(card)))
			continue
		}
		if warning, ok := unavailable[slot]; ok {
			copy := warning
			slots = append(slots, SlotResult{
				Slot:        slot,
				Unavailable: &copy,
			})
			continue
		}
		message := defaultUnavailableMessage(slot)
		if slot == SlotConnect {
			message = defaultConnectUnavailableMessage(hasContinue)
		}
		slots = append(slots, unavailableSlot(slot, message))
	}
	return DailyBrief{
		Date:  date,
		Slots: slots,
	}
}

func defaultUnavailableMessage(slot SlotKind) string {
	switch slot {
	case SlotContinue:
		return "No substantive notes are available."
	case SlotRediscover:
		return "No rediscover candidate is available."
	case SlotConnect:
		return defaultConnectUnavailableMessage(true)
	default:
		return "No recommendation is available."
	}
}

func defaultConnectUnavailableMessage(hasContinue bool) string {
	if !hasContinue {
		return "Continue source is not available for Connect."
	}
	return "No connect candidate is available."
}

func cardBySlot(cards []Card) map[SlotKind]Card {
	bySlot := make(map[SlotKind]Card, len(cards))
	for _, card := range cards {
		bySlot[card.Slot] = cloneCard(card)
	}
	return bySlot
}

func materializeSlotCard(date LocalDate, result SlotResult) (Card, bool, error) {
	if result.Card == nil {
		return Card{}, false, nil
	}
	card := cloneCard(*result.Card)
	candidatePath := ""
	if card.Candidate != nil {
		candidatePath = card.Candidate.RelPath
	}
	cardID, err := CardIDFor(date, result.Slot, card.Source.RelPath, candidatePath)
	if err != nil {
		return Card{}, false, err
	}
	card.ID = cardID
	card.Slot = result.Slot
	card.Excerpt = BoundCardExcerpt(card.Excerpt)
	return card, true, nil
}

func materializedSlotResult(result SlotResult, card Card, hasCard bool) SlotResult {
	if !hasCard {
		return result
	}
	return cardSlot(result.Slot, card)
}

func copyUnavailable(in map[SlotKind]UnavailableSlot) map[SlotKind]UnavailableSlot {
	if len(in) == 0 {
		return make(map[SlotKind]UnavailableSlot)
	}
	out := make(map[SlotKind]UnavailableSlot, len(in))
	for slot, warning := range in {
		out[slot] = warning
	}
	return out
}

func unavailableFromResult(result SlotResult, fallback string) UnavailableSlot {
	if result.Unavailable != nil {
		return *result.Unavailable
	}
	return UnavailableSlot{
		Slot:    result.Slot,
		Message: fallback,
	}
}

func appendWarning(warnings *[]ServiceWarning, warning ServiceWarning) {
	if warnings == nil {
		return
	}
	for _, existing := range *warnings {
		if existing.Code == warning.Code && existing.Message == warning.Message {
			return
		}
	}
	*warnings = append(*warnings, warning)
}

func stateUnreadableWarning(err error) ServiceWarning {
	return ServiceWarning{
		Code:    WarningStateUnreadable,
		Message: fmt.Sprintf("brief state unavailable: %v", err),
	}
}

func corpusPathSet(corpus CorpusSnapshot) (map[string]struct{}, error) {
	paths := make(map[string]struct{}, len(corpus.Notes))
	for _, note := range corpus.Notes {
		normalized, err := normalizeBriefPath(note.Ref.RelPath)
		if err != nil {
			return nil, fmt.Errorf("brief: corpus note path %q: %w", note.Ref.RelPath, err)
		}
		paths[normalized] = struct{}{}
	}
	return paths, nil
}

func pathInCorpus(paths map[string]struct{}, relPath string) bool {
	normalized, err := normalizeBriefPath(relPath)
	if err != nil {
		return false
	}
	_, ok := paths[normalized]
	return ok
}
