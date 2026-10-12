// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import { historyRangeLabel } from "./history-workspace-list";
import { historyChangeDurability } from "./history-workspace-restore";
import {
  cleanup,
  waitForExpect,
  jsonResponse,
  renderWorkspace,
  listBody,
  startWorkspace,
  listOnly,
} from "./history-workspace.test-helpers";

afterEach(cleanup);

describe("history URL navigation", () => {
  it("preserves exact paths across reload and pagination, and resets the page when matching changes", async () => {
    const path = "Folder/雪 & A#.md";
    window.history.replaceState(null, "", `/history?${new URLSearchParams({ path, path_match: "exact", offset: "20" })}`);
    renderWorkspace();
    const calls = startWorkspace((url) => {
      const offset = Number(new URL(url, window.location.origin).searchParams.get("offset"));
      return jsonResponse(listBody({ offset, total: 60, has_more: offset < 40 }));
    });
    await waitForExpect(() => expect(document.querySelectorAll("[data-history-entry]")).toHaveLength(1));
    expect(new URL(calls[0].url, window.location.origin).searchParams.get("path")).toBe(path);
    const mode = document.querySelector<HTMLSelectElement>("[data-history-path-match]")!;
    expect(mode.value).toBe("exact");
    document.querySelector<HTMLButtonElement>("[data-history-next]")!.click();
    await waitForExpect(() => expect(calls).toHaveLength(2));
    const next = new URL(calls[1].url, window.location.origin).searchParams;
    expect(next.get("offset")).toBe("40");
    expect(next.get("path_match")).toBe("exact");
    expect(new URLSearchParams(window.location.search).get("path_match")).toBe("exact");
    mode.value = "contains";
    mode.dispatchEvent(new Event("change", { bubbles: true }));
    await waitForExpect(() => expect(calls).toHaveLength(3));
    const changed = new URL(calls[2].url, window.location.origin).searchParams;
    expect(changed.has("offset")).toBe(false);
    expect(changed.has("path_match")).toBe(false);
    expect(changed.get("path")).toBe(path);
  });

  it("loads bookmarked filters before their vocabulary arrives and retains the page", async () => {
    window.history.replaceState(null, "", "/history?path=Folder%2FA+%26+B%23.md&origin=import&origin=browser&class=note&limit=50&offset=100");
    renderWorkspace();
    const calls = startWorkspace(listOnly(listBody({ offset: 100, limit: 50, total: 150 })));
    await waitForExpect(() => expect(document.querySelectorAll("[data-history-entry]")).toHaveLength(1));
    expect(calls[0]?.url).toBe("/api/history?limit=50&offset=100&origin=import&origin=browser&class=note&path=Folder%2FA+%26+B%23.md");
    expect(document.querySelector<HTMLInputElement>("[data-history-path]")?.value).toBe("Folder/A & B#.md");
    expect(document.querySelectorAll("[data-history-origin-filters] input:checked")).toHaveLength(2);
    expect(document.querySelector<HTMLSelectElement>("[data-history-page-size-select]")?.value).toBe("50");
    expect(new URLSearchParams(window.location.search).get("offset")).toBe("100");
    expect(document.querySelector("[data-history-entry] .history-paths li > span")?.textContent).toBe("Source.md");
    expect(document.querySelector("[data-history-entry] .history-paths a")?.getAttribute("href"))
      .toBe("/history?path=Source.md&path_match=exact");
  });

  it("restores controls and requests on browser Back and Forward", async () => {
    window.history.replaceState(null, "", "/history?path=Source.md&path_match=exact");
    renderWorkspace();
    const calls = startWorkspace(listOnly(listBody()));
    await waitForExpect(() => expect(document.querySelectorAll("[data-history-entry]")).toHaveLength(1));
    document.querySelector<HTMLButtonElement>("[data-history-clear-filters]")?.click();
    await waitForExpect(() => expect(calls).toHaveLength(2));
    expect(new URLSearchParams(window.location.search).has("path")).toBe(false);
    window.history.back();
    await waitForExpect(() => expect(calls).toHaveLength(3));
    expect(calls[2]?.url).toBe("/api/history?limit=20&path=Source.md&path_match=exact");
    expect(document.querySelector<HTMLSelectElement>("[data-history-path-match]")?.value).toBe("exact");
    expect(document.querySelector<HTMLInputElement>("[data-history-path]")?.value).toBe("Source.md");
    window.history.forward();
    await waitForExpect(() => expect(calls).toHaveLength(4));
    expect(document.querySelector<HTMLInputElement>("[data-history-path]")?.value).toBe("");
    expect(document.querySelector<HTMLSelectElement>("[data-history-path-match]")?.value).toBe("contains");
  });

  it("normalizes invalid pagination without dropping the note filter", async () => {
    window.history.replaceState(null, "", "/history?path=Source.md&limit=999999&offset=-1");
    renderWorkspace();
    const calls = startWorkspace(listOnly(listBody()));
    await waitForExpect(() => expect(document.querySelectorAll("[data-history-entry]")).toHaveLength(1));
    expect(calls[0]?.url).toBe("/api/history?limit=20&path=Source.md");
    expect(window.location.search).toBe("?limit=20&path=Source.md");
  });

  it("cancels pending typing when browser navigation restores another view", async () => {
    renderWorkspace();
    const calls = startWorkspace(listOnly(listBody()));
    await waitForExpect(() => expect(document.querySelectorAll("[data-history-entry]")).toHaveLength(1));
    vi.useFakeTimers();
    const path = document.querySelector<HTMLInputElement>("[data-history-path]")!;
    path.value = "unfinished";
    path.dispatchEvent(new Event("input", { bubbles: true }));
    window.history.replaceState(null, "", "/history?path=Source.md&offset=20");
    window.dispatchEvent(new PopStateEvent("popstate"));
    await vi.advanceTimersByTimeAsync(500);
    expect(calls).toHaveLength(2);
    expect(calls[1]?.url).toBe("/api/history?limit=20&offset=20&path=Source.md");
    expect(path.value).toBe("Source.md");
  });
});

describe("history workspace rendering", () => {
  it("renders the server's filter vocabulary, the entries and the range", async () => {
    renderWorkspace();
    startWorkspace(listOnly(listBody({ total: 3, has_more: true })));

    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-history-entry]")).toHaveLength(1);
    });
    expect(
      Array.from(document.querySelectorAll("[data-history-origin-filters] input")).map(
        (input) => (input as HTMLInputElement).value
      )
    ).toEqual(["browser", "import"]);
    expect(document.querySelector("[data-history-origin-filters]")?.textContent).toContain(
      "Imported from your other replica"
    );
    expect(document.querySelector("[data-history-entry-origin]")?.textContent).toBe("Browser");
    expect(document.querySelector("[data-history-range]")?.textContent).toBe("1-1 of 3");
    expect(document.querySelector<HTMLButtonElement>("[data-history-previous]")?.disabled).toBe(true);
    expect(document.querySelector<HTMLButtonElement>("[data-history-next]")?.disabled).toBe(false);
    expect(document.querySelector("[data-history-status]")?.textContent).toBe(
      "Showing 1 of 3 recorded changes."
    );
  });

  it("reports unreadable entries above the list and counts the ones it cannot show", async () => {
    renderWorkspace();
    startWorkspace(listOnly(listBody({
      warnings: [{ id: "entry-bad", message: "manifest is unreadable" }],
      warning_count: 4,
    })));

    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-history-entry]")).toHaveLength(1);
    });
    const warnings = document.querySelector<HTMLElement>("[data-history-warnings]");
    expect(warnings?.hidden).toBe(false);
    expect(warnings?.textContent).toContain("entry-bad: manifest is unreadable");
    expect(warnings?.textContent).toContain("3 more unreadable entries are not listed here.");
  });

  it("says a filtered view is empty rather than calling the journal empty", async () => {
    window.history.replaceState(null, "", "/history?path=Missing");
    renderWorkspace();
    startWorkspace(listOnly(listBody({ entries: [], total: 0 })));

    await waitForExpect(() => {
      expect(document.querySelector("[data-history-status]")?.textContent).toContain(
        "No recorded change matches these filters."
      );
    });
    expect(document.querySelector("[data-history-range]")?.textContent).toBe("0 entries");
  });

  it("labels a window rather than a page count", () => {
    expect(historyRangeLabel(0, 0, 0)).toBe("0 entries");
    expect(historyRangeLabel(40, 0, 12)).toBe("No entries at this position of 12.");
    expect(historyRangeLabel(20, 20, 51)).toBe("21-40 of 51");
  });

  it("separates being recorded here from travelling to the other replica", () => {
    const base = {
      path: "A.md",
      current_state: "file",
      restored_state: "file",
      class: "note",
      conflict: false,
    };
    expect(historyChangeDurability({ ...base, recorded: true, travels: true })).toContain(
      "travels to your other replica"
    );
    expect(historyChangeDurability({ ...base, recorded: true, travels: false })).toContain(
      "stays on this machine"
    );
    expect(historyChangeDurability({ ...base, recorded: false, travels: false })).toBe(
      "not recorded in the content history"
    );
  });
});

describe("history workspace filtering and paging", () => {
  it("sends the checked filters and the typed path as one query", async () => {
    renderWorkspace();
    const calls = startWorkspace(listOnly(listBody()));
    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-history-origin-filters] input")).toHaveLength(2);
    });

    const origin = document.querySelector<HTMLInputElement>(
      "[data-history-origin-filters] input[value=import]"
    );
    const klass = document.querySelector<HTMLInputElement>(
      "[data-history-class-filters] input[value=attachment]"
    );
    const path = document.querySelector<HTMLInputElement>("[data-history-path]");
    if (!origin || !klass || !path) {
      throw new Error("filters missing");
    }
    origin.checked = true;
    klass.checked = true;
    path.value = "Inbox/";
    document.querySelector("[data-history-filters]")?.dispatchEvent(
      new Event("change", { bubbles: true })
    );

    await waitForExpect(() => {
      expect(calls).toHaveLength(2);
    });
    expect(calls[1]?.url).toBe(
      "/api/history?limit=20&origin=import&class=attachment&path=Inbox%2F"
    );
  });

  it("pages by the size that is actually selected", async () => {
    renderWorkspace();
    const calls = startWorkspace(listOnly(listBody({ total: 120, has_more: true })));
    await waitForExpect(() => {
      expect(calls).toHaveLength(1);
    });

    const size = document.querySelector<HTMLSelectElement>("[data-history-page-size-select]");
    if (!size) {
      throw new Error("page size select missing");
    }
    size.value = "50";
    document.querySelector<HTMLButtonElement>("[data-history-next]")?.click();

    await waitForExpect(() => {
      expect(calls).toHaveLength(2);
    });
    expect(calls[1]?.url).toBe("/api/history?limit=50&offset=50");
  });

  it("clears every filter at once", async () => {
    renderWorkspace();
    const calls = startWorkspace(listOnly(listBody()));
    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-history-origin-filters] input")).toHaveLength(2);
    });

    const origin = document.querySelector<HTMLInputElement>(
      "[data-history-origin-filters] input[value=browser]"
    );
    const path = document.querySelector<HTMLInputElement>("[data-history-path]");
    if (!origin || !path) {
      throw new Error("filters missing");
    }
    origin.checked = true;
    path.value = "Inbox/";
    document.querySelector<HTMLButtonElement>("[data-history-clear-filters]")?.click();

    await waitForExpect(() => {
      expect(calls).toHaveLength(2);
    });
    expect(calls[1]?.url).toBe("/api/history?limit=20");
    expect(origin.checked).toBe(false);
    expect(path.value).toBe("");
  });
});
