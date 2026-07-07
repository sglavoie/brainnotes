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
  fixOpenKeyFromRelPath,
  initDailyFixes,
} from "./daily-fixes";

const waitForExpect = async (assertion: () => void): Promise<void> => {
  let lastError: unknown;
  for (let attempt = 0; attempt < 20; attempt += 1) {
    try {
      assertion();
      return;
    } catch (error) {
      lastError = error;
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
  }
  throw lastError;
};

const jsonResponse = (body: unknown, status = 200): Response =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

const brokenTask = (overrides: Partial<CockpitTask> = {}): CockpitTask => ({
  id: "task_broken",
  signature: "sig-broken",
  kind: "broken_target",
  priority: 30,
  title: "Broken target: missing",
  summary: "1 unresolved link to missing",
  detail: "missing",
  count: 1,
  affected: [{
    title: "Source",
    rel_path: "Work/Source.md",
    folder: "Work",
    location: "line 5",
  }],
  actions: [
    { kind: "open", label: "Open note" },
    { kind: "relink", label: "Relink" },
    { kind: "create_stub", label: "Create stub note" },
    { kind: "snooze", label: "Snooze" },
    { kind: "mark_done", label: "Mark done" },
  ],
  triage_only: false,
  ...overrides,
});

const orphanTask = (overrides: Partial<CockpitTask> = {}): CockpitTask => ({
  id: "task_orphan",
  signature: "sig-orphan",
  kind: "orphan_note",
  priority: 50,
  title: "Orphan note",
  summary: "No inbound links",
  detail: "",
  count: 1,
  affected: [{ title: "Lonely", rel_path: "Lonely.md" }],
  actions: [
    { kind: "open", label: "Open note" },
    { kind: "snooze", label: "Snooze" },
    { kind: "mark_done", label: "Mark done" },
  ],
  triage_only: true,
  ...overrides,
});

const sensitiveTask = (overrides: Partial<CockpitTask> = {}): CockpitTask => ({
  id: "task_sensitive",
  signature: "sig-sensitive",
  kind: "sensitive_content",
  priority: 15,
  title: "Possible sensitive content: Secrets",
  summary: "Possible secret detected",
  detail: "Work/Secrets.md",
  count: 1,
  affected: [{
    title: "Secrets",
    rel_path: "Work/Secrets.md",
    folder: "Work",
    location: "line 12: AWS access key ID",
  }],
  actions: [
    { kind: "open", label: "Open note" },
    { kind: "snooze", label: "Snooze" },
    { kind: "mark_done", label: "Mark done" },
  ],
  triage_only: true,
  ...overrides,
});

const queuePayload = (tasks: CockpitTask[], message = ""): unknown => ({
  ok: true,
  message: message || undefined,
  tasks,
  counts: {
    active: tasks.length,
    done_hidden: 2,
    snoozed_hidden: 1,
    total_generated: tasks.length + 3,
  },
  warnings: [{ message: "Cockpit state could not be loaded." }],
});

const renderPanel = (csrfToken = "csrf-token"): void => {
  document.body.innerHTML = `
    <section
      data-fixes-panel
      data-fixes-endpoint="/api/cockpit"
      data-fixes-item-endpoint="/api/cockpit/item?view=daily"
      data-fixes-preview-endpoint="/api/fixes/preview"
      data-fixes-apply-endpoint="/api/fixes/apply"
      data-fixes-open-endpoint="/api/open"
      data-fixes-csrf="${csrfToken}"
      aria-busy="false"
    >
      <dd data-fixes-count-active>0</dd>
      <dd data-fixes-count-done-hidden>0</dd>
      <dd data-fixes-count-snoozed-hidden>0</dd>
      <dd data-fixes-count-total-generated>0</dd>
      <p data-fixes-status></p>
      <p data-fixes-loading hidden></p>
      <p data-fixes-error hidden></p>
      <p data-fixes-empty hidden></p>
      <ul data-fixes-warnings hidden></ul>
      <ul data-fixes-list hidden></ul>
    </section>
  `;
};

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

  it("summarizes active counts", () => {
    expect(fixCountsStatusMessage({ active: 0, done_hidden: 0, snoozed_hidden: 0, total_generated: 0 })).toBe("No active fixes.");
    expect(fixCountsStatusMessage({ active: 1, done_hidden: 0, snoozed_hidden: 0, total_generated: 1 })).toBe("1 active fix.");
    expect(fixCountsStatusMessage({ active: 3, done_hidden: 0, snoozed_hidden: 0, total_generated: 3 })).toBe("3 active fixes.");
  });
});

describe("daily fixes panel DOM behavior", () => {
  it("loads grouped tasks, counts, warnings, and open keys", async () => {
    renderPanel();
    const fetchMock = vi.fn(async () => jsonResponse(queuePayload([brokenTask(), orphanTask()])));

    initDailyFixes({ fetch: fetchMock as typeof fetch });
    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-fixes-task]")).toHaveLength(2);
    });

    expect(document.querySelectorAll("[data-fixes-group]")).toHaveLength(2);
    expect(document.querySelector("[data-fixes-count-active]")?.textContent).toBe("2");
    expect(document.querySelector("[data-fixes-count-done-hidden]")?.textContent).toBe("2");
    expect(document.querySelector("[data-fixes-warning]")?.textContent).toBe("Cockpit state could not be loaded.");
    expect(document.querySelector("[data-fixes-status]")?.textContent).toBe("2 active fixes.");

    // Only the broken-target (non-triage) task advertises a fix action.
    expect(document.querySelectorAll("[data-fixes-fix-action]")).toHaveLength(2);
    expect(
      Array.from(document.querySelectorAll('[data-fixes-id="task_broken"] [data-fixes-fix-action]')).map(
        (b) => b.getAttribute("data-fixes-fix-action")
      )
    ).toEqual(["relink", "create_stub"]);
    expect(document.querySelectorAll('[data-fixes-id="task_orphan"] [data-fixes-fix-action]')).toHaveLength(0);
    expect(document.querySelector("[data-fixes-open-key]")?.getAttribute("data-fixes-open-key")).toBe("work/source");
  });

  it("renders advisory findings with labels, grouping, and no fix buttons", async () => {
    renderPanel();
    const advisoryKinds: Array<[string, string]> = [
      ["sensitive_content", "Sensitive content"],
      ["missing_review_date", "Missing review date"],
      ["stale_status", "Stale status"],
      ["wip_limit", "WIP limit"],
      ["hub_needs_synthesis", "Hub needs synthesis"],
      ["ai_daily_review", "AI daily review"],
    ];
    const tasks = advisoryKinds.map(([kind], index) =>
      sensitiveTask({
        id: `task_${kind}`,
        signature: `sig-${kind}`,
        kind,
        priority: 15 + index,
      })
    );
    const fetchMock = vi.fn(async () => jsonResponse(queuePayload(tasks)));

    initDailyFixes({ fetch: fetchMock as typeof fetch });
    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-fixes-task]")).toHaveLength(advisoryKinds.length);
    });

    // Each advisory kind gets its own group with the human-readable label.
    const headings = Array.from(document.querySelectorAll(".fixes-group-heading")).map(
      (h) => h.textContent
    );
    for (const [, label] of advisoryKinds) {
      expect(headings).toContain(`${label} (1)`);
    }

    // Triage-only rows are marked and never advertise a mechanical fix button.
    expect(document.querySelectorAll("[data-fixes-triage]")).toHaveLength(advisoryKinds.length);
    expect(document.querySelectorAll("[data-fixes-fix-action]")).toHaveLength(0);

    // Sensitive findings surface only the redacted, server-provided location.
    expect(document.querySelector('[data-fixes-id="task_sensitive_content"] .diagnostic-location')?.textContent)
      .toBe("line 12: AWS access key ID");
  });

  it("previews and applies a fix, then reloads the queue", async () => {
    renderPanel();
    let queueCalls = 0;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === "/api/fixes/preview") {
        expect(String(init?.body)).toBe("id=task_broken&action=relink&token=csrf-token");
        return jsonResponse({
          ok: true,
          message: "Preview: fix ready to apply.",
          action: "relink",
          task_id: "task_broken",
          previews: [{
            rel_path: "Work/Source.md",
            changes: [{
              kind: "body",
              line: 1,
              old: { present: true, text: "[[Missing]]" },
              new: { present: true, text: "[[Target]]" },
            }],
          }],
          preview_id: "pv-1",
          expires_at: "2026-07-04T12:00:00Z",
        });
      }
      if (url === "/api/fixes/apply") {
        expect(String(init?.body)).toBe("id=task_broken&action=relink&token=csrf-token&preview_id=pv-1");
        return jsonResponse({
          ok: true,
          message: "Updated 1 note.",
          action: "relink",
          task_id: "task_broken",
          changed: ["Work/Source.md"],
          created: [],
          journal_id: "j1",
        });
      }
      queueCalls += 1;
      return jsonResponse(queueCalls === 1 ? queuePayload([brokenTask()]) : queuePayload([]));
    });

    initDailyFixes({ fetch: fetchMock as typeof fetch });
    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-fixes-task]")).toHaveLength(1);
    });

    document.querySelector<HTMLButtonElement>('[data-fixes-fix-action="relink"]')?.click();
    await waitForExpect(() => {
      expect(document.querySelector(".fixes-preview-changes li")?.textContent).toBe(
        "body:1: [[Missing]] -> [[Target]]"
      );
    });
    expect(document.querySelector("[data-fixes-status]")?.textContent).toBe("Preview: fix ready to apply.");

    document.querySelector<HTMLButtonElement>("[data-fixes-preview-apply]")?.click();
    await waitForExpect(() => {
      expect(document.querySelector("[data-fixes-status]")?.textContent).toBe("Updated 1 note.");
    });
    expect(document.querySelectorAll("[data-fixes-task]")).toHaveLength(0);
    expect(document.querySelector<HTMLElement>("[data-fixes-empty]")?.hidden).toBe(false);
    expect(fetchMock.mock.calls.some((call) => String(call[0]) === "/api/fixes/apply")).toBe(true);
  });

  it("posts triage actions and renders the returned queue", async () => {
    renderPanel();
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input) === "/api/cockpit/item?view=daily") {
        expect(String(init?.body)).toBe("id=task_orphan&action=snooze&token=csrf-token&snooze=week");
        return jsonResponse({
          ok: true,
          message: "Snoozed task for a week.",
          tasks: [],
          counts: { active: 0, done_hidden: 2, snoozed_hidden: 2, total_generated: 4 },
          warnings: [],
        });
      }
      return jsonResponse(queuePayload([orphanTask()]));
    });

    initDailyFixes({ fetch: fetchMock as typeof fetch });
    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-fixes-task]")).toHaveLength(1);
    });

    document.querySelector<HTMLButtonElement>(
      '[data-fixes-item-action="snooze"][data-fixes-snooze="week"]'
    )?.click();
    await waitForExpect(() => {
      expect(document.querySelector("[data-fixes-status]")?.textContent).toBe("Snoozed task for a week.");
    });
    expect(document.querySelectorAll("[data-fixes-task]")).toHaveLength(0);
    expect(document.querySelector("[data-fixes-count-snoozed-hidden]")?.textContent).toBe("2");
  });

  it("surfaces request errors", async () => {
    renderPanel();
    const fetchMock = vi.fn(async () =>
      jsonResponse({ ok: false, code: "internal", message: "Cockpit failed." }, 500)
    );

    initDailyFixes({ fetch: fetchMock as typeof fetch });
    await waitForExpect(() => {
      expect(document.querySelector("[data-fixes-error]")?.textContent).toBe("Cockpit failed.");
    });
    expect(document.querySelector<HTMLElement>("[data-fixes-error]")?.hidden).toBe(false);
    expect(document.querySelector("[data-fixes-status]")?.classList.contains("is-error")).toBe(true);
  });

  it("refreshes after management mutations", async () => {
    renderPanel();
    const fetchMock = vi.fn(async () => {
      if (fetchMock.mock.calls.length === 1) {
        return jsonResponse(queuePayload([brokenTask({ title: "First" })]));
      }
      return jsonResponse(queuePayload([brokenTask({ title: "Refreshed" })]));
    });

    initDailyFixes({ fetch: fetchMock as typeof fetch });
    await waitForExpect(() => {
      expect(document.querySelector(".review-item-title")?.textContent).toBe("First");
    });

    window.dispatchEvent(new CustomEvent(managementMutationEventName));
    await waitForExpect(() => {
      expect(document.querySelector(".review-item-title")?.textContent).toBe("Refreshed");
    });
    expect(document.querySelector("[data-fixes-status]")?.textContent).toBe("Fixes refreshed.");
  });
});
