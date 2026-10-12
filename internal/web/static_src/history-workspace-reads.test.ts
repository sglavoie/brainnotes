// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import { initHistoryWorkspace } from "./history-workspace";
import {
  cleanup,
  waitForExpect,
  jsonResponse,
  renderWorkspace,
  entry,
  listBody,
  previewBody,
  startWorkspace,
  listOnly,
  statusText,
  statusClass,
  clickRefresh,
  restoreWithDeferredReads,
  deferredPreviewWorkspace,
  settlePreview,
} from "./history-workspace.test-helpers";

afterEach(cleanup);

// Every filter checkbox, both pager buttons and Refresh start a request at once,
// and nothing cancels the one already in flight, so two are ordinary and their
// replies can arrive in either order.
describe("history workspace overlapping page reads", () => {
  it("ignores a page reply that a newer request has already superseded", async () => {
    renderWorkspace();
    const pending: Array<(body: unknown) => void> = [];
    const fetchMock = vi.fn(
      (input: RequestInfo | URL) =>
        new Promise<Response>((resolve) => {
          pending.push((body) => {
            resolve(jsonResponse(body));
          });
          void input;
        })
    );
    initHistoryWorkspace({ fetch: fetchMock as unknown as typeof fetch });

    await waitForExpect(() => {
      expect(pending).toHaveLength(1);
    });
    pending[0](listBody({ total: 60, has_more: true }));
    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-history-preview-entry]")).toHaveLength(1);
    });

    // Two clicks, two requests in flight, neither cancelled.
    document.querySelector<HTMLButtonElement>("[data-history-next]")?.click();
    document.querySelector<HTMLButtonElement>("[data-history-next]")?.click();
    await waitForExpect(() => {
      expect(pending).toHaveLength(3);
    });

    // The newest answers first; the older one lands afterwards and must change
    // nothing, or the page would be showing the second window while the pager
    // and the offset describe the third.
    pending[2](listBody({ entries: [entry("entry-newest")], total: 60, offset: 40, has_more: true }));
    await waitForExpect(() => {
      expect(document.querySelector("[data-history-range]")?.textContent).toContain("41");
    });
    pending[1](listBody({ entries: [entry("entry-stale")], total: 60, offset: 20, has_more: true }));

    await new Promise((resolve) => setTimeout(resolve, 5));
    expect(document.querySelector("[data-history-range]")?.textContent).toContain("41");
    // The identifier lives in an attribute, not in the rendered text, so it is
    // read as one — asserting against textContent would pass either way.
    expect(
      document.querySelector("[data-history-list] [data-history-entry]")?.getAttribute("data-history-entry")
    ).toBe("entry-newest");

    // And the offset the stale reply carried must not be what the next page
    // counts on from.
    document.querySelector<HTMLButtonElement>("[data-history-next]")?.click();
    await waitForExpect(() => {
      expect(fetchMock.mock.calls.at(-1)?.[0]).toContain("offset=60");
    });
  });
});

// Every other status this page shows restates something already on screen. The
// outcome of a restore is the one report of something that happened once, so it
// is held on the state until a render puts it up — and these are the three
// things that holding it has to get right.
describe("history workspace write outcomes", () => {
  // The read it travels on is as interruptible as any other: a Refresh or a
  // filter click landing inside that window used to drop the report and leave
  // the ordinary "Showing N of M" line in its place.
  it("still reports a restore whose page read was overtaken by a newer one", async () => {
    const pendingLists = await restoreWithDeferredReads();

    clickRefresh();
    await waitForExpect(() => {
      expect(pendingLists).toHaveLength(3);
    });
    pendingLists[1](listBody());
    pendingLists[2](listBody());

    await waitForExpect(() => {
      expect(statusText()).toBe("Undo restored 1 path.");
    });
    expect(statusClass()).toContain("is-success");
  });

  // Held until it is shown, and no longer: a message that outlived its render
  // would reappear on every later read, announcing a restore that happened once
  // as though it kept happening.
  it("reports a restore once and then lets the page describe itself again", async () => {
    const pendingLists = await restoreWithDeferredReads();
    pendingLists[1](listBody());
    await waitForExpect(() => {
      expect(statusText()).toBe("Undo restored 1 path.");
    });

    clickRefresh();
    await waitForExpect(() => {
      expect(pendingLists).toHaveLength(3);
    });
    pendingLists[2](listBody());
    await waitForExpect(() => {
      expect(statusText()).toBe("Showing 1 of 1 recorded changes.");
    });
  });

  // A read that failed reported nothing, so it must not consume the report. The
  // restore still happened, and the next read is the first chance to say so.
  it("keeps a restore's outcome when the read that would have shown it fails", async () => {
    const pendingLists = await restoreWithDeferredReads();
    pendingLists[1]({ ok: false, code: "internal", message: "History read failed." }, 500);
    await waitForExpect(() => {
      expect(statusClass()).toContain("is-error");
    });

    clickRefresh();
    await waitForExpect(() => {
      expect(pendingLists).toHaveLength(3);
    });
    pendingLists[2](listBody());
    await waitForExpect(() => {
      expect(statusText()).toBe("Undo restored 1 path.");
    });
  });
});

it("keeps the last selected preview when earlier success or failure arrives late", async () => {
  const { pending, buttons } = await deferredPreviewWorkspace();
  buttons[0].click();
  buttons[0].click();
  buttons[1].click();
  expect(document.activeElement).toBe(document.querySelector("[data-history-restore-heading]"));
  expect(document.querySelector<HTMLButtonElement>("[data-history-restore-confirm]")?.disabled).toBe(true);
  await settlePreview(pending[2], previewBody({ entry_id: "entry-2", preview_id: "preview-2" }));
  await settlePreview(pending[0], previewBody());
  await settlePreview(pending[1], { ok: false, message: "Old failure" });
  expect(document.querySelector("[data-history-restore-facts]")?.textContent).toContain("entry-2");
  expect(document.querySelector("[data-history-status]")?.textContent).toBe("Previewed. Nothing has changed yet.");
});

it("cancels a pending preview, returns focus, and ignores its late response", async () => {
  const { pending, buttons, calls } = await deferredPreviewWorkspace();
  buttons[0].click();
  document.querySelector<HTMLButtonElement>("[data-history-restore-cancel]")!.click();
  expect(document.activeElement).toBe(buttons[0]);
  await settlePreview(pending[0], previewBody());
  expect(document.querySelector<HTMLElement>("[data-history-restore]")?.hidden).toBe(true);
  expect(document.querySelector("[data-history-status]")?.textContent).toBe("Restore canceled. Nothing was changed.");
  expect(calls.some((call) => call.url === "/api/undo")).toBe(false);
});

it("invalidates pending previews when the history list changes", async () => {
  const { pending, buttons } = await deferredPreviewWorkspace();
  buttons[0].click();
  document.querySelector<HTMLButtonElement>("[data-history-refresh]")!.click();
  await settlePreview(pending[0], previewBody());
  expect(document.querySelector<HTMLElement>("[data-history-restore]")?.hidden).toBe(true);
  expect(document.querySelector("[data-history-status]")?.textContent).toContain("Showing");
});

it("disables the old receipt while a replacement preview is pending", async () => {
  const { pending, buttons, calls } = await deferredPreviewWorkspace();
  buttons[0].click();
  await settlePreview(pending[0], previewBody());
  buttons[1].click();
  const confirm = document.querySelector<HTMLButtonElement>("[data-history-restore-confirm]")!;
  expect(confirm.disabled).toBe(true);
  expect(document.querySelector("[data-history-restore-facts]")?.textContent).toBe("");
  confirm.click();
  expect(calls.some((call) => call.url === "/api/undo")).toBe(false);
});

it("explains a history with no recorded changes", async () => {
  renderWorkspace();
  startWorkspace(listOnly(listBody({ entries: [], total: 0 })));
  await waitForExpect(() => expect(document.querySelector("[data-history-status]")?.textContent)
    .toContain("No changes have been recorded yet."));
  expect(document.querySelector("[data-history-status] button")).toBeNull();
});

it("does not describe unreadable history as never recorded", async () => {
  renderWorkspace();
  startWorkspace(listOnly(listBody({ entries: [], total: 0, warning_count: 1,
    warnings: [{ id: "broken", message: "Cannot read entry" }] })));
  await waitForExpect(() => expect(document.querySelector("[data-history-status]")?.textContent)
    .toContain("No readable changes to show."));
});

it("clears an empty filtered view from its nearby action", async () => {
  window.history.replaceState(null, "", "/history?path=Missing&origin=import&class=attachment&offset=20");
  renderWorkspace();
  const calls = startWorkspace((url) => jsonResponse(url.includes("path=")
    ? listBody({ entries: [], total: 0 }) : listBody()));
  await waitForExpect(() => expect(document.querySelector("[data-history-status] button")?.textContent).toBe("Clear filters"));
  document.querySelector<HTMLButtonElement>("[data-history-status] button")!.click();
  await waitForExpect(() => expect(document.querySelector("[data-history-status]")?.textContent).toContain("Showing"));
  expect(calls[1].url).toBe("/api/history?limit=20");
  expect(window.location.search).toBe("?limit=20");
  expect(document.activeElement).toBe(document.querySelector("[data-history-clear-filters]"));
});

it("keeps the empty-state action mounted when the path field loses focus", async () => {
  window.history.replaceState(null, "", "/history?path=Missing");
  renderWorkspace();
  const calls = startWorkspace(listOnly(listBody({ entries: [], total: 0 })));
  await waitForExpect(() => expect(document.querySelector("[data-history-status] button")).not.toBeNull());
  const action = document.querySelector<HTMLButtonElement>("[data-history-status] button")!;
  document.querySelector("[data-history-path]")!.dispatchEvent(new Event("change", { bubbles: true }));
  expect(action.isConnected).toBe(true);
  expect(calls).toHaveLength(1);
  action.click();
  expect(calls).toHaveLength(2);
  expect(calls[1].url).toBe("/api/history?limit=20");
});
