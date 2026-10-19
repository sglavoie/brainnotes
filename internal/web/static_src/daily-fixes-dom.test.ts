// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import { managementMutationEventName } from "./api";
import { initDailyFixes } from "./daily-fixes";
import {
  brokenTask,
  deadEndTask,
  jsonResponse,
  longTailTask,
  orphanTask,
  queuePayload,
  renderPanel,
  sensitiveTask,
  waitForExpect,
} from "./daily-fixes.test-helpers";

afterEach(() => {
  document.body.innerHTML = "";
  vi.unstubAllGlobals();
});

describe("daily fixes panel rendering", () => {
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
    const openButton = document.querySelector("[data-fixes-open-key]");
    expect(openButton?.getAttribute("data-fixes-open-key")).toBe("work/source");
    // The second attribute is what hides this button in a read-only replica:
    // static/capabilities.css hides [data-fixes-button] there, and the Open
    // posts to /api/open, which the replica's POST allowlist refuses. Drop the
    // attribute and the button stays on the page and answers 403. Nothing else
    // in the tree asserts the pair — the Go test over that stylesheet can only
    // read the stylesheet.
    expect(openButton?.hasAttribute("data-fixes-button")).toBe(true);
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

});

describe("daily fixes panel previewed and triaged actions", () => {
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
        expect(String(init?.body)).toMatch(/^id=task_broken&action=relink&token=csrf-token&preview_id=pv-1&operation_id=[0-9a-f]{32}$/);
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

});

describe("daily fixes panel unpreviewed immediate actions", () => {
  it("marks a dead-end note as intentional immediately", async () => {
    renderPanel();
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input) === "/api/cockpit/item?view=daily") {
        expect(String(init?.body)).toBe(
          "id=task_dead_end&action=exempt_dead_end&token=csrf-token"
        );
        return jsonResponse({
          ok: true,
          message: "Marked Terminal as an intentional dead end.",
          tasks: [],
          counts: { active: 0, done_hidden: 0, snoozed_hidden: 0, total_generated: 0 },
          warnings: [],
        });
      }
      return jsonResponse(queuePayload([deadEndTask()]));
    });
    const mutationListener = vi.fn();
    window.addEventListener(managementMutationEventName, mutationListener, { once: true });

    initDailyFixes({ fetch: fetchMock as typeof fetch });
    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-fixes-task]")).toHaveLength(1);
    });

    const button = document.querySelector<HTMLButtonElement>(
      '[data-fixes-item-action="exempt_dead_end"]'
    );
    expect(button?.textContent).toBe("Mark as intentional dead end");
    expect(button?.classList.contains("fixes-immediate-action")).toBe(true);
    button?.click();

    await waitForExpect(() => {
      expect(document.querySelector("[data-fixes-status]")?.textContent).toBe(
        "Marked Terminal as an intentional dead end."
      );
    });
    expect(document.querySelectorAll("[data-fixes-task]")).toHaveLength(0);
    expect(mutationListener).toHaveBeenCalledTimes(1);
  });

  it("removes a long-tail property from every affected note immediately", async () => {
    renderPanel();
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input) === "/api/cockpit/item?view=daily") {
        expect(String(init?.body)).toBe(
          "id=task_long_tail&action=clean_long_tail&token=csrf-token"
        );
        return jsonResponse({
          ok: true,
          message: 'Removed property "status" from 2 affected notes.',
          tasks: [],
          counts: { active: 0, done_hidden: 0, snoozed_hidden: 0, total_generated: 0 },
          warnings: [],
        });
      }
      return jsonResponse(queuePayload([longTailTask()]));
    });

    initDailyFixes({ fetch: fetchMock as typeof fetch });
    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-fixes-task]")).toHaveLength(1);
    });

    const button = document.querySelector<HTMLButtonElement>(
      '[data-fixes-item-action="clean_long_tail"]'
    );
    expect(button?.textContent).toBe("Remove property from affected notes");
    expect(button?.classList.contains("fixes-immediate-action")).toBe(true);
    button?.click();

    await waitForExpect(() => {
      expect(document.querySelector("[data-fixes-status]")?.textContent).toBe(
        'Removed property "status" from 2 affected notes.'
      );
    });
    expect(document.querySelectorAll("[data-fixes-task]")).toHaveLength(0);
  });

});

describe("daily fixes panel errors and refresh", () => {
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
    expect(document.querySelector("[data-fixes-status]")?.textContent).toBe("");
  });

  it("names the Reload fixes control when the queue cannot be reached", async () => {
    renderPanel();
    initDailyFixes({ fetch: vi.fn(async () => { throw new Error("offline"); }) as typeof fetch });
    await waitForExpect(() => {
      expect(document.querySelector("[data-fixes-error]")?.textContent).toBe(
        "Couldn't load the fixes queue. Check your connection, then select Reload fixes."
      );
    });
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
