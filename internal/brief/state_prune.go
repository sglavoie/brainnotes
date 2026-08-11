package brief

import (
	"time"
)

func pruneBriefState(state *briefStateFile, paths map[string]struct{}, at time.Time) {
	if state == nil {
		return
	}
	cutoff := at.Add(-HistoryRetention)
	for dateKey, selection := range state.DailySelections {
		date, err := normalizeLocalDate(LocalDate(dateKey))
		if err != nil {
			delete(state.DailySelections, dateKey)
			continue
		}
		dateTime, err := parseLocalDateIn(date, at.Location())
		if err != nil || dateTime.Before(startOfLocalDay(cutoff)) {
			delete(state.DailySelections, dateKey)
			continue
		}
		cards := make([]Card, 0, len(selection.Cards))
		for _, card := range selection.Cards {
			if !containsBriefPath(paths, card.Source.RelPath) {
				continue
			}
			if card.Candidate != nil && !containsBriefPath(paths, card.Candidate.RelPath) {
				continue
			}
			cards = append(cards, card)
		}
		if len(cards) == 0 {
			delete(state.DailySelections, dateKey)
			continue
		}
		selection.Cards = cards
		selection.Date = date
		state.DailySelections[dateKey] = selection
	}
	for relPath, ts := range state.NoteExposures {
		if !containsBriefPath(paths, relPath) || ts.Before(cutoff) {
			delete(state.NoteExposures, relPath)
		}
	}
	for pairID, record := range state.PairExposures {
		if !containsBriefPath(paths, record.SourceRelPath) ||
			!containsBriefPath(paths, record.CandidateRelPath) ||
			record.ExposedAt.Before(cutoff) {
			delete(state.PairExposures, pairID)
		}
	}
	for visitDate := range state.Visits {
		parsed, err := parseLocalDateIn(LocalDate(visitDate), at.Location())
		if err != nil || parsed.Before(startOfLocalDay(cutoff)) {
			delete(state.Visits, visitDate)
		}
	}
	prunedOpens := make([]briefOpenRecord, 0, len(state.Opens))
	for _, open := range state.Opens {
		if open.OpenedAt.Before(cutoff) {
			continue
		}
		prunedOpens = append(prunedOpens, open)
	}
	state.Opens = prunedOpens
}

func containsBriefPath(paths map[string]struct{}, wanted string) bool {
	_, ok := paths[wanted]
	return ok
}
