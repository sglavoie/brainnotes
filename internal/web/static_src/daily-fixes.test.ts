// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import {
  buildCockpitItemForm,
  buildFixApplyForm,
  managementMutationEventName,
  normalizeCockpitQueueResponse,
  normalizeFixPreviewResponse,
  type CockpitTask,
} from "./api";
import {
  buildFixesApplyFetchRequest,
  buildFixesItemFetchRequest,
  fixCountsStatusMessage,
  fixKindLabel,
  fixOpenKeyFromRelPath,
  initDailyFixes,
} from "./daily-fixes";

afterEach(() => {
  document.body.innerHTML = "";
  vi.unstubAllGlobals();
});

describe("daily fixes helpers", () => {
  it("normalizes cockpit queue payloads", () => {
    const result = normalizeCockpitQueueResponse({
      ok: "true",
      message: "Updated",
      tasks: [{
        id: " task_1 ",
        signature: " sig ",
        kind: " broken_target ",
        priority: "30",
        title: " Broken ",
        summary: " Summary ",
        detail: " Detail ",
        count: "2",
        affected: [{
          title: " Source ",
          rel_path: " Work/Source.md ",
          folder: " Work ",
          location: " line 5 ",
        }, {}],
        actions: [{ kind: " relink ", label: " Relink " }, { kind: "" }],
        triage_only: "false",
      }, { id: "" }],
      counts: { active: "1", done_hidden: "2", snoozed_hidden: "3", total_generated: "4" },
      warnings: [{ message: " warn " }, {}],
    });

    expect(result.ok).toBe(true);
    expect(result.tasks).toHaveLength(1);
    expect(result.tasks[0]).toMatchObject({
      id: "task_1",
      kind: "broken_target",
      priority: 30,
      count: 2,
      triage_only: false,
    });
    expect(result.tasks[0].affected).toEqual([{
      title: "Source",
      rel_path: "Work/Source.md",
      folder: "Work",
      location: "line 5",
    }]);
    expect(result.tasks[0].actions).toEqual([{ kind: "relink", label: "Relink" }]);
    expect(result.counts).toEqual({
      active: 1,
      done_hidden: 2,
      snoozed_hidden: 3,
      total_generated: 4,
    });
    expect(result.warnings).toEqual([{ message: "warn", path: undefined }]);
  });

  it("normalizes fix preview payloads", () => {
    const result = normalizeFixPreviewResponse({
      ok: true,
      action: "relink",
      task_id: "task_broken",
      previews: [{
        rel_path: "Work/Source.md",
        changes: [{
          kind: "body",
          line: "1",
          old: { present: true, text: "[[Missing]]" },
          new: { present: true, text: "[[Target]]" },
        }],
      }],
      skipped: [{ target: "Missing", reason: "ambiguous" }],
      preview_id: "pv-1",
      expires_at: "2026-07-04T12:00:00Z",
    });

    expect(result.ok).toBe(true);
    expect(result.preview_id).toBe("pv-1");
    expect(result.previews[0].changes[0]).toMatchObject({
      kind: "body",
      line: 1,
    });
    expect(result.skipped).toEqual([{ rel_path: undefined, target: "Missing", reason: "ambiguous" }]);
  });

  it("builds cockpit and fix request forms", () => {
    expect(buildCockpitItemForm({
      id: "task_1",
      action: "snooze",
      token: "csrf",
      snooze: "week",
    }).toString()).toBe("id=task_1&action=snooze&token=csrf&snooze=week");

    expect(buildFixApplyForm({
      id: "task_1",
      action: "relink",
      token: "csrf",
      previewID: "pv-1",
    }).toString()).toBe("id=task_1&action=relink&token=csrf&preview_id=pv-1");

    const itemRequest = buildFixesItemFetchRequest("/api/cockpit/item", "csrf", "task_1", "mark_done");
    expect(itemRequest.url).toBe("/api/cockpit/item");
    expect(itemRequest.init.body).toBe("id=task_1&action=mark_done&token=csrf");

    const dailyItemRequest = buildFixesItemFetchRequest("/api/cockpit/item?view=daily", "csrf", "task_1", "mark_done");
    expect(dailyItemRequest.url).toBe("/api/cockpit/item?view=daily");
    expect(dailyItemRequest.init.body).toBe("id=task_1&action=mark_done&token=csrf");

    const applyRequest = buildFixesApplyFetchRequest("/api/fixes/apply", "csrf", "task_1", "relink", "pv-1");
    expect(applyRequest.init.body).toBe("id=task_1&action=relink&token=csrf&preview_id=pv-1");
  });

  it("derives safe open keys from rel paths", () => {
    expect(fixOpenKeyFromRelPath("Work/Source.md")).toBe("work/source");
    expect(fixOpenKeyFromRelPath("Notes\\Deep\\Idea.md")).toBe("notes/deep/idea");
    expect(fixOpenKeyFromRelPath("Bad/../escape.md")).toBe("");
    expect(fixOpenKeyFromRelPath("")).toBe("");
  });

  it("labels every known rule kind in lockstep with cockpit.KindLabel", () => {
    // The Go side is TestKindLabelMatchesWebVocabulary in
    // internal/cockpit/reviewcontext_test.go; both tables must list the same
    // kinds so a rule never renders as the generic "Fix" in one surface only.
    expect({
      frontmatter_error: fixKindLabel("frontmatter_error"),
      schema_violation: fixKindLabel("schema_violation"),
      broken_target: fixKindLabel("broken_target"),
      duplicate_title: fixKindLabel("duplicate_title"),
      orphan_note: fixKindLabel("orphan_note"),
      dead_end_note: fixKindLabel("dead_end_note"),
      long_tail_value: fixKindLabel("long_tail_value"),
      sensitive_content: fixKindLabel("sensitive_content"),
      missing_review_date: fixKindLabel("missing_review_date"),
      stale_status: fixKindLabel("stale_status"),
      wip_limit: fixKindLabel("wip_limit"),
      hub_needs_synthesis: fixKindLabel("hub_needs_synthesis"),
      capture_backlog: fixKindLabel("capture_backlog"),
      ai_daily_review: fixKindLabel("ai_daily_review"),
    }).toEqual({
      frontmatter_error: "Frontmatter",
      schema_violation: "Schema",
      broken_target: "Broken target",
      duplicate_title: "Duplicate title",
      orphan_note: "Orphan",
      dead_end_note: "Dead end",
      long_tail_value: "Long-tail value",
      sensitive_content: "Sensitive content",
      missing_review_date: "Missing review date",
      stale_status: "Stale status",
      wip_limit: "WIP limit",
      hub_needs_synthesis: "Hub needs synthesis",
      capture_backlog: "Capture backlog",
      ai_daily_review: "AI daily review",
    });
    expect(fixKindLabel("nope")).toBe("Fix");
  });

  it("summarizes active counts", () => {
    expect(fixCountsStatusMessage({ active: 0, done_hidden: 0, snoozed_hidden: 0, total_generated: 0 })).toBe("No active fixes.");
    expect(fixCountsStatusMessage({ active: 1, done_hidden: 0, snoozed_hidden: 0, total_generated: 1 })).toBe("1 active fix.");
    expect(fixCountsStatusMessage({ active: 3, done_hidden: 0, snoozed_hidden: 0, total_generated: 3 })).toBe("3 active fixes.");
  });
});
