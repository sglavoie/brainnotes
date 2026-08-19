package recall

import "time"

func evaluateRecall(createdAt time.Time, signal RecallSignal, now time.Time) recallMatch {
	match := recallMatch{
		suppressed: signal.Dismissed || (!signal.SnoozedUntil.IsZero() && signal.SnoozedUntil.After(now)),
		useful:     signal.Useful,
	}
	if match.suppressed {
		return match
	}

	if !createdAt.IsZero() && createdAt.Before(now) {
		match.age = now.Sub(createdAt)
		match.score = min(float64(match.age)/float64(FullNoveltyAge), 1)
		match.old = match.age >= OldNoteAge
	}
	if signal.Useful && match.score < UsefulRecallBoost {
		match.score = UsefulRecallBoost
	}

	match.notRecentlyOpened = signal.LastOpenedAt.IsZero()
	if !signal.LastOpenedAt.IsZero() {
		match.recentOpenAge = now.Sub(signal.LastOpenedAt)
		if match.recentOpenAge >= RecentOpenWindow {
			match.notRecentlyOpened = true
		} else {
			factor := max(float64(match.recentOpenAge)/float64(RecentOpenWindow), 0)
			match.score *= factor
		}
	}
	return match
}
