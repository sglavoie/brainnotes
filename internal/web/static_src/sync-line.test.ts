// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from "vitest";
import { initSyncLine, renderSyncLine } from "./sync-line";
import { syncJSON, syncLineMarkup, syncStatus } from "./sync.test-helpers";
import type { FetchLike } from "./sync-requests";

// The one line every page carries. Two of its answers are decisions rather than
// renderings: a mode whose guard refuses the route leaves the line empty, and a
// server that failed to answer says so — and the line is read again when the tab
// is looked at again rather than on a timer.

const lineElement = (): HTMLElement => {
  const line = document.querySelector<HTMLElement>("[data-sync-line]");
  if (!line) {
    throw new Error("the fixture has no sync line");
  }
  return line;
};

const setVisibility = (state: "visible" | "hidden"): void => {
  Object.defineProperty(document, "visibilityState", { value: state, configurable: true });
  document.dispatchEvent(new Event("visibilitychange"));
};

beforeEach(() => {
  document.body.innerHTML = syncLineMarkup();
  setVisibility("visible");
});

it("writes the server's own sentence and links to where the decision is made", () => {
  const line = lineElement();

  renderSyncLine(
    line,
    {
      ok: true,
      result: syncStatus({
        summary: "2 changes are waiting for a decision.",
        needs_person: true,
        review_path: "/sync",
      }),
    },
    document
  );

  expect(line.textContent).toContain("Sync: 2 changes need review.");
  expect(line.classList.contains("is-attention")).toBe(true);
  const review = line.querySelector<HTMLAnchorElement>("a.sync-line-review");
  expect(review?.getAttribute("href")).toBe("/sync");
  expect(review?.textContent).toBe("Review");
});

it("offers no review link when the server names no review path", () => {
  const line = lineElement();

  renderSyncLine(line, { ok: true, result: syncStatus() }, document);

  expect(line.querySelector("a.sync-line-review")).toBeNull();
  expect(line.classList.contains("is-attention")).toBe(false);
});

it("leaves the line empty on a mode that refuses the route", () => {
  const line = lineElement();
  line.classList.add("is-attention");

  for (const code of ["forbidden", "not_found"]) {
    renderSyncLine(line, { ok: false, code, message: "this server does not offer that" }, document);
    // A refusal is a decision somebody made, not a fault: a nav that reported it
    // on every page would be shouting about a working server.
    expect(line.textContent).toBe("");
    expect(line.classList.contains("is-attention")).toBe(false);
  }
});

it("says a status it could not read is unavailable, and says why", () => {
  const line = lineElement();

  renderSyncLine(line, { ok: false, code: "internal", message: "the state file is damaged" }, document);

  expect(line.textContent).toBe("Synchronization status is unavailable. the state file is damaged");
  expect(line.classList.contains("is-attention")).toBe(true);
});

it("reads once and reads again when the tab is looked at again", async () => {
  const fetcher = vi.fn<FetchLike>(async () =>
    syncJSON(syncStatus({ summary: "Everything is packaged." }))
  );

  initSyncLine({ doc: document, fetch: fetcher });
  await vi.waitFor(() => expect(lineElement().textContent).toContain("Everything is packaged."));
  expect(fetcher).toHaveBeenCalledTimes(1);
  expect(fetcher.mock.calls[0][0]).toBe("/api/sync/status");

  // A hidden tab is nobody reading: the question is not worth asking.
  setVisibility("hidden");
  expect(fetcher).toHaveBeenCalledTimes(1);

  setVisibility("visible");
  await vi.waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2));
});

it("asks nothing on a page whose line names no endpoint", async () => {
  document.body.innerHTML = syncLineMarkup("");
  const fetcher = vi.fn(async () => syncJSON(syncStatus()));

  initSyncLine({ doc: document, fetch: fetcher });
  await Promise.resolve();

  expect(fetcher).not.toHaveBeenCalled();
});
