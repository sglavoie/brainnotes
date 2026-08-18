package cockpit

// KindLabel returns the short human label for a rule kind. It mirrors the
// fixKindLabel vocabulary in internal/web/static_src/daily-fixes-model.ts so the CLI,
// the web UI, and the daily-review context all name a rule the same way.
// Unknown kinds fall back to "Fix", matching the TypeScript default.
func KindLabel(kind RuleKind) string {
	switch kind {
	case RuleFrontmatterError:
		return "Frontmatter"
	case RuleSchemaViolation:
		return "Schema"
	case RuleBrokenTarget:
		return "Broken target"
	case RuleDuplicateTitle:
		return "Duplicate title"
	case RuleOrphanNote:
		return "Orphan"
	case RuleDeadEndNote:
		return "Dead end"
	case RuleLongTailValue:
		return "Long-tail value"
	case RuleSensitiveContent:
		return "Sensitive content"
	case RuleMissingReviewDate:
		return "Missing review date"
	case RuleStaleStatus:
		return "Stale status"
	case RuleWIPLimit:
		return "WIP limit"
	case RuleHubNeedsSynthesis:
		return "Hub needs synthesis"
	case RuleCaptureBacklog:
		return "Capture backlog"
	case RuleAIDailyReview:
		return "AI daily review"
	default:
		return "Fix"
	}
}

// ReviewContext is a deterministic, redacted snapshot of a cockpit queue, shaped
// for read-only inspection and for handing to an optional review agent (session
// 9). It is derived purely from an already-sorted, workflow-filtered, and
// daily-limited Queue: it copies only human-safe fields (titles, summaries, and
// the server-redacted per-note locations) and never carries raw note bodies or
// file hashes. Because it is a pure function of its input queue, the same queue
// always yields the identical context — no clock or randomness is involved.
type ReviewContext struct {
	// Counts mirrors the queue's workflow counts so a reviewer sees the full
	// active/done-hidden/snoozed-hidden totals, not just the surfaced slice.
	Counts Counts `json:"counts"`
	// TaskCount is the number of tasks surfaced in this context (len(Items)).
	// It can be smaller than Counts.Active when the queue was daily-limited.
	TaskCount int `json:"task_count"`
	// Groups summarizes the surfaced tasks by rule kind, in the order each kind
	// first appears in the queue (its priority order).
	Groups []ReviewGroup `json:"groups"`
	// Items is the ordered, redacted list of surfaced tasks.
	Items []ReviewItem `json:"items"`
}

// ReviewGroup counts the surfaced tasks of one rule kind, with its short label.
type ReviewGroup struct {
	Kind  RuleKind `json:"kind"`
	Label string   `json:"label"`
	Count int      `json:"count"`
}

// ReviewItem is one task rendered into the review context. It carries only the
// redacted, human-facing fields of a Task — never the file hashes that anchor a
// task signature, and never any raw matched evidence.
type ReviewItem struct {
	Kind       RuleKind     `json:"kind"`
	Label      string       `json:"label"`
	Priority   int          `json:"priority"`
	Title      string       `json:"title"`
	Summary    string       `json:"summary"`
	Detail     string       `json:"detail,omitempty"`
	Count      int          `json:"count"`
	TriageOnly bool         `json:"triage_only"`
	Affected   []ReviewNote `json:"affected,omitempty"`
}

// ReviewNote is an affected note in the review context. Location is copied
// verbatim from AffectedNote.Location, which upstream rules already redact (for
// sensitive_content it is the "line N: <detector category>" string, with no raw
// match), so the context needs no further redaction step.
type ReviewNote struct {
	Title    string `json:"title"`
	RelPath  string `json:"rel_path"`
	Folder   string `json:"folder,omitempty"`
	Location string `json:"location,omitempty"`
}

// BuildReviewContext derives the deterministic daily-review context from an
// already-prepared queue. Callers pass the queue after workflow-state filtering
// and daily limiting (see StateStore.FilterQueue and LimitDaily) so the context
// reflects exactly the tasks a reviewer would see. The queue's task order is
// preserved; groups are emitted in first-appearance order. No AI command is
// invoked — this is the read-only substrate session 9 will feed to an agent.
func BuildReviewContext(queue Queue) ReviewContext {
	ctx := ReviewContext{
		Counts:    queue.Counts,
		TaskCount: len(queue.Tasks),
		Items:     make([]ReviewItem, 0, len(queue.Tasks)),
	}

	groupIndex := map[RuleKind]int{}
	for _, task := range queue.Tasks {
		affected := make([]ReviewNote, 0, len(task.Affected))
		for _, note := range task.Affected {
			affected = append(affected, ReviewNote{
				Title:    note.Title,
				RelPath:  note.RelPath,
				Folder:   note.Folder,
				Location: note.Location,
			})
		}
		ctx.Items = append(ctx.Items, ReviewItem{
			Kind:       task.Kind,
			Label:      KindLabel(task.Kind),
			Priority:   task.Priority,
			Title:      task.Title,
			Summary:    task.Summary,
			Detail:     task.Detail,
			Count:      task.Count,
			TriageOnly: task.TriageOnly,
			Affected:   affected,
		})

		if idx, ok := groupIndex[task.Kind]; ok {
			ctx.Groups[idx].Count++
			continue
		}
		groupIndex[task.Kind] = len(ctx.Groups)
		ctx.Groups = append(ctx.Groups, ReviewGroup{
			Kind:  task.Kind,
			Label: KindLabel(task.Kind),
			Count: 1,
		})
	}

	return ctx
}
