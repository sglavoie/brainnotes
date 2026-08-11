package brief

import (
	"fmt"
	"time"
)

func parseLocalDateIn(date LocalDate, location *time.Location) (time.Time, error) {
	date, err := normalizeLocalDate(date)
	if err != nil {
		return time.Time{}, err
	}
	if location == nil {
		location = time.Local
	}
	parsed, err := time.ParseInLocation("2006-01-02", string(date), location)
	if err != nil {
		return time.Time{}, fmt.Errorf("brief: parse local date %q: %w", date, err)
	}
	return parsed, nil
}

func rollingActivityForState(state briefStateFile, at time.Time) (RollingActivity, error) {
	summary7, err := activitySummaryForWindow(state, at, 7)
	if err != nil {
		return RollingActivity{}, err
	}
	summary14, err := activitySummaryForWindow(state, at, 14)
	if err != nil {
		return RollingActivity{}, err
	}
	return RollingActivity{
		Last7Days:  summary7,
		Last14Days: summary14,
	}, nil
}

func activitySummaryForWindow(state briefStateFile, at time.Time, windowDays int) (ActivitySummary, error) {
	today := startOfLocalDay(at)
	visitDaySet := make(map[string]struct{}, len(state.Visits))
	openCount := 0
	for visitDate := range state.Visits {
		day, err := parseLocalDateIn(LocalDate(visitDate), at.Location())
		if err != nil {
			return ActivitySummary{}, err
		}
		if dayWithinWindow(day, today, windowDays) {
			visitDaySet[visitDate] = struct{}{}
		}
	}
	for _, open := range state.Opens {
		day := startOfLocalDay(open.OpenedAt.In(at.Location()))
		if dayWithinWindow(day, today, windowDays) {
			openCount++
		}
	}
	return ActivitySummary{
		WindowDays: windowDays,
		VisitDays:  len(visitDaySet),
		OpenCount:  openCount,
	}, nil
}

func dayWithinWindow(day, today time.Time, windowDays int) bool {
	if windowDays <= 0 {
		return false
	}
	if day.After(today) {
		return false
	}
	delta := int(today.Sub(day).Hours() / 24)
	return delta >= 0 && delta < windowDays
}

func startOfLocalDay(ts time.Time) time.Time {
	location := ts.Location()
	if location == nil {
		location = time.Local
	}
	local := ts.In(location)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
}
