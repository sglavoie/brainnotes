package journal

import (
	"fmt"
	"os"
	"sort"
	"time"
)

// RetentionPolicy controls journal pruning.
type RetentionPolicy struct {
	// MaxEntries keeps only the newest N entries. Zero disables this rule.
	MaxEntries int
	// MaxAge removes entries older than Now()-MaxAge. Zero disables this rule.
	MaxAge time.Duration
	// Now overrides the clock for deterministic tests.
	Now func() time.Time
}

// Prune removes old entry directories according to policy.
//
// Entries are selected deterministically from the manifest timestamp (oldest
// first, then ID) and removed in that same order.
func Prune(journalRoot string, policy RetentionPolicy) ([]string, error) {
	if policy.MaxEntries < 0 {
		return nil, fmt.Errorf("journal: max entries cannot be negative")
	}
	if policy.MaxAge < 0 {
		return nil, fmt.Errorf("journal: max age cannot be negative")
	}
	if policy.MaxEntries == 0 && policy.MaxAge == 0 {
		return nil, nil
	}
	nowFn := policy.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn().UTC()

	listed, err := listEntries(journalRoot)
	if err != nil {
		return nil, err
	}

	type doomed struct {
		id string
		ts time.Time
	}
	doomedByID := make(map[string]doomed)

	if policy.MaxEntries > 0 {
		for i := policy.MaxEntries; i < len(listed); i++ {
			e := listed[i]
			doomedByID[e.entry.ID] = doomed{id: e.entry.ID, ts: e.entry.Timestamp.UTC()}
		}
	}
	if policy.MaxAge > 0 {
		cutoff := now.Add(-policy.MaxAge)
		for _, e := range listed {
			if e.entry.Timestamp.UTC().Before(cutoff) {
				doomedByID[e.entry.ID] = doomed{id: e.entry.ID, ts: e.entry.Timestamp.UTC()}
			}
		}
	}

	doomedList := make([]doomed, 0, len(doomedByID))
	for _, d := range doomedByID {
		doomedList = append(doomedList, d)
	}
	sort.Slice(doomedList, func(i, j int) bool {
		if !doomedList[i].ts.Equal(doomedList[j].ts) {
			return doomedList[i].ts.Before(doomedList[j].ts)
		}
		return doomedList[i].id < doomedList[j].id
	})

	removed := make([]string, 0, len(doomedList))
	for _, d := range doomedList {
		dir, err := entryDir(journalRoot, d.id)
		if err != nil {
			return nil, fmt.Errorf("journal: prune %q: %w", d.id, err)
		}
		if err := os.RemoveAll(dir); err != nil {
			return nil, fmt.Errorf("journal: prune %q: %w", d.id, err)
		}
		removed = append(removed, d.id)
	}
	return removed, nil
}
