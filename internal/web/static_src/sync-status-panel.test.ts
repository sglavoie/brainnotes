// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from "vitest";
import { createSyncStatusPanel } from "./sync-status-panel";
import { syncJSON, syncStatus, syncStatusPanelMarkup, syncWorker } from "./sync.test-helpers";
import type { FetchLike } from "./sync-requests";

// The itemized status on /sync.
//
// What is tested here is not the wording of the facts — sync-status-view.test.ts
// owns that — but the panel's three claims about the vault: it dates what it
// shows, it stops vouching for facts it could not re-read, and it asks the server
// again rather than adjusting a count in the browser.
//
// Most observations use a literal timestamp so the assertions do not depend
// on the machine locale. Clock progression is exercised separately below.

const hostOf = (selector: string): HTMLElement => {
  const host = document.querySelector<HTMLElement>(selector);
  if (!host) {
    throw new Error(`the fixture has no ${selector}`);
  }
  return host;
};

const panelRoot = (): HTMLElement => hostOf("[data-sync-panel]");
const summary = (): HTMLElement => hostOf("[data-sync-summary]");
const facts = (): HTMLElement => hostOf("[data-sync-facts]");
const problems = (): HTMLElement => hostOf("[data-sync-problems]");

const terms = (): string[] =>
  Array.from(facts().querySelectorAll("dt")).map((term) => term.textContent ?? "");

const details = (): string[] =>
  Array.from(facts().querySelectorAll("dd")).map((detail) => detail.textContent ?? "");

const listed = (): string[] =>
  Array.from(problems().querySelectorAll("li")).map((item) => item.textContent ?? "");

const answering = (...payloads: unknown[]) => {
  let call = 0;
  return vi.fn<FetchLike>(async () => {
    const payload = payloads[Math.min(call, payloads.length - 1)];
    call += 1;
    return syncJSON(payload);
  });
};

beforeEach(() => {
  document.body.innerHTML = syncStatusPanelMarkup();
});

it("itemizes the facts and says when they were read", async () => {
  const fetcher = answering(syncStatus({ observed_at: "whenever" }));
  const panel = createSyncStatusPanel({ root: panelRoot(), fetch: fetcher });

  await panel?.load();

  expect(fetcher.mock.calls[0][0]).toBe("/api/sync/status");
  expect(summary().textContent).toBe("Everything is up to date.");
  expect(summary().classList.contains("is-success")).toBe(true);
  expect(hostOf("[data-sync-checked]").textContent).toBe("Checked whenever.");
  expect(hostOf("[data-sync-details]").hasAttribute("open")).toBe(false);
  expect(terms()).toEqual([
    "Saved here",
    "Packaged for the other replica",
    "Acknowledged by the other replica",
    "Arrived from the other replica",
  ]);
  expect(details()).toHaveLength(4);
  // Nothing is in the way, so the list is not an empty box on the page.
  expect(problems().hidden).toBe(true);
  expect(listed()).toEqual([]);
});

it("says nothing about when it read a status the server did not date", async () => {
  const panel = createSyncStatusPanel({
    root: panelRoot(),
    fetch: answering(syncStatus({ observed_at: "" })),
  });

  await panel?.load();

  expect(summary().textContent).toBe("Everything is up to date.");
});

it("reports a vault that needs a person as a warning rather than a success", async () => {
  const panel = createSyncStatusPanel({
    root: panelRoot(),
    fetch: answering(
      syncStatus({
        observed_at: "whenever",
        summary: "2 changes are waiting for a decision.",
        needs_person: true,
        peer: { conflicts: 2 },
      })
    ),
  });

  await panel?.load();

  expect(summary().textContent).toBe("2 changes are waiting for a decision.");
  expect(hostOf("[data-sync-details]").hasAttribute("open")).toBe(true);
  expect(summary().classList.contains("is-warning")).toBe(true);
  expect(summary().classList.contains("is-success")).toBe(false);
  // The fact that needs attention is marked as such on both halves of the pair,
  // because a styled term above an unstyled detail is half a sentence.
  const conflicts = facts().querySelectorAll(".sync-fact.is-problem");
  expect(conflicts).toHaveLength(2);
  expect(conflicts[0].textContent).toBe("Arrived from the other replica");
});

it("lists everything standing in the way", async () => {
  const panel = createSyncStatusPanel({
    root: panelRoot(),
    fetch: answering(
      syncStatus({
        observed_at: "whenever",
        problems: [{ subject: "export", detail: "the companion is not running" }],
        workers: [syncWorker({ name: "export", failures: 2, last_error: "no route to host" })],
      })
    ),
  });

  await panel?.load();

  expect(problems().hidden).toBe(false);
  expect(listed()).toHaveLength(2);
  expect(listed()[0]).toBe("export: the companion is not running");
  expect(listed()[1]).toContain("no route to host");
});

it("stops vouching for facts it could not read again", async () => {
  const fetcher = vi
    .fn<FetchLike>()
    .mockResolvedValueOnce(
      syncJSON(
        syncStatus({
          observed_at: "whenever",
          problems: [{ subject: "", detail: "the companion is not running" }],
        })
      )
    )
    .mockRejectedValueOnce(new Error("the socket closed"));
  const panel = createSyncStatusPanel({ root: panelRoot(), fetch: fetcher });

  await panel?.load();
  expect(terms()).toHaveLength(4);
  expect(listed()).toHaveLength(1);

  await panel?.load();

  // A panel keeping the last good reading on screen beside an error would be
  // showing a moment it can no longer name, which is the one thing this surface
  // must not do: these counts are what a person decides from.
  expect(summary().textContent).toBe("The server did not answer.");
  expect(summary().classList.contains("is-error")).toBe(true);
  expect(summary().classList.contains("is-success")).toBe(false);
  expect(terms()).toEqual([]);
  expect(listed()).toEqual([]);
  expect(problems().hidden).toBe(true);
});

it("says it is reading while it reads", async () => {
  let answer: (resp: Response) => void = () => {};
  const fetcher = vi.fn<FetchLike>(
    () =>
      new Promise<Response>((resolve) => {
        answer = resolve;
      })
  );
  const panel = createSyncStatusPanel({ root: panelRoot(), fetch: fetcher });

  const reading = panel?.load();
  expect(summary().textContent).toBe("Reading synchronization status…");
  expect(summary().classList.contains("is-loading")).toBe(true);

  answer(syncJSON(syncStatus({ observed_at: "whenever" })));
  await reading;

  expect(summary().classList.contains("is-loading")).toBe(false);
});

it("asks the server again when the refresh button is pressed", async () => {
  const fetcher = answering(
    syncStatus({ observed_at: "whenever", summary: "1 change is waiting to be applied." }),
    syncStatus({ observed_at: "whenever" })
  );
  const panel = createSyncStatusPanel({ root: panelRoot(), fetch: fetcher });
  await panel?.load();
  expect(summary().textContent).toContain("1 change is waiting to be applied.");

  hostOf("[data-sync-refresh]").click();

  await vi.waitFor(() => expect(summary().textContent).toBe("Everything is up to date."));
  expect(fetcher).toHaveBeenCalledTimes(2);
});

it("is no panel at all when the page it was given has nowhere to put a fact", async () => {
  facts().remove();
  const fetcher = answering(syncStatus());

  // Returning null rather than rendering what it can: a page missing a host is a
  // template that changed, and a panel that half-rendered would report a partial
  // status as the whole of it.
  expect(createSyncStatusPanel({ root: panelRoot(), fetch: fetcher })).toBeNull();
  expect(fetcher).not.toHaveBeenCalled();
});

it("opens pending transport details even when no human decision is needed", async () => {
  const panel = createSyncStatusPanel({
    root: panelRoot(), fetch: answering(syncStatus({ needs_person: false, export: { pending_commits: 2 } })),
  });
  await panel?.load();
  expect(hostOf("[data-sync-details]").hasAttribute("open")).toBe(true);
  panel?.dispose();
});

it("ignores a superseded response instead of restoring stale facts", async () => {
  let answerFirst: (response: Response) => void = () => {};
  const fetcher = vi.fn<FetchLike>()
    .mockImplementationOnce(() => new Promise((resolve) => { answerFirst = resolve; }))
    .mockResolvedValueOnce(syncJSON(syncStatus({ observed_at: "newest" })));
  const panel = createSyncStatusPanel({ root: panelRoot(), fetch: fetcher });
  const first = panel?.load();
  await panel?.load();
  answerFirst(syncJSON(syncStatus({ summary: "Old problem", needs_person: true, observed_at: "old" })));
  await first;
  expect(summary().textContent).toBe("Everything is up to date.");
  expect(hostOf("[data-sync-checked]").textContent).toBe("Checked newest.");
  panel?.dispose();
});

it("ages the observation without claiming a new server check", async () => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date("2026-10-18T12:00:00Z"));
  const panel = createSyncStatusPanel({
    root: panelRoot(), fetch: answering(syncStatus({ observed_at: "2026-10-18T12:00:00Z" })),
  });
  try {
    await panel?.load();
    expect(hostOf("[data-sync-checked]").textContent).toBe("Checked just now.");
    vi.advanceTimersByTime(120_000);
    expect(hostOf("[data-sync-checked]").textContent).toBe("Checked 2 minutes ago.");
    expect(hostOf("[data-sync-checked]").getAttribute("datetime")).toBe("2026-10-18T12:00:00Z");
  } finally {
    panel?.dispose();
    vi.useRealTimers();
  }
});
