// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import { initHistoryWorkspace } from "./history-workspace";
import { historyRangeLabel } from "./history-workspace-list";
import { historyChangeDurability } from "./history-workspace-restore";

const waitForExpect = async (assertion: () => void): Promise<void> => {
  let lastError: unknown;
  for (let attempt = 0; attempt < 40; attempt += 1) {
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

// The markup mirrors internal/web/templates/history.html. The page test in
// internal/web/history_page_test.go is what holds the two in step: it names
// every attribute this file reads, so a template that stopped emitting one
// fails there rather than leaving this suite testing a page nobody serves.
const renderWorkspace = (): void => {
  document.body.innerHTML = `
    <div data-history-workspace
      data-history-endpoint="/api/history"
      data-history-preview-endpoint="/api/history/preview"
      data-history-undo-endpoint="/api/undo"
      data-history-csrf="csrf-token"
      data-history-page-size="20"
      data-history-max-page-size="200">
      <section>
        <button type="button" data-history-refresh>Refresh</button>
        <button type="button" data-history-clear-filters>Clear filters</button>
        <form data-history-filters>
          <label><span>Path</span>
            <input type="search" name="path" data-history-path>
          </label>
          <label><span>Path matching</span>
            <select data-history-path-match>
              <option value="contains">Contains</option>
              <option value="exact">Exact path</option>
            </select>
          </label>
          <fieldset data-history-origin-filters><legend>Where it came from</legend></fieldset>
          <fieldset data-history-class-filters><legend>What it touched</legend></fieldset>
          <label><span>Entries per page</span>
            <select data-history-page-size-select>
              <option value="20">20</option>
              <option value="50">50</option>
            </select>
          </label>
        </form>
      </section>
      <section>
        <p data-history-status></p>
        <ul data-history-warnings></ul>
        <ul data-history-list></ul>
        <nav data-history-pager>
          <button type="button" data-history-previous>Newer</button>
          <p data-history-range></p>
          <button type="button" data-history-next>Older</button>
        </nav>
      </section>
      <section data-history-restore hidden>
        <h2 data-history-restore-heading tabindex="-1">Restore this change</h2>
        <button type="button" data-history-restore-cancel>Cancel</button>
        <p data-history-restore-summary></p>
        <dl data-history-restore-facts></dl>
        <ul data-history-restore-changes></ul>
        <div data-history-restore-conflicts hidden>
          <ul data-history-restore-conflict-list></ul>
          <label><input type="checkbox" data-history-restore-consent><span>I understand.</span></label>
        </div>
        <button type="button" data-history-restore-confirm>Restore</button>
      </section>
    </div>
  `;
};

const expectReadyPreview = (): void => {
  expect(document.querySelector<HTMLElement>("[data-history-restore]")?.hidden).toBe(false);
  expect(document.querySelector("[data-history-restore]")?.getAttribute("aria-busy")).toBe("false");
};

const ORIGINS = [
  { value: "browser", label: "Browser" },
  { value: "import", label: "Imported from your other replica" },
];

const CLASSES = [
  { value: "note", label: "Notes" },
  { value: "attachment", label: "Attachments" },
];

const entry = (id: string, overrides: Record<string, unknown> = {}): unknown => ({
  id,
  timestamp: "2026-01-02T03:04:05Z",
  command: "bn web frontmatter set",
  origin: "browser",
  origin_label: "Browser",
  file_count: 1,
  classes: [{ class: "note", label: "Notes", count: 1 }],
  files: [{ path: "Source.md", before: "file", after: "file", class: "note", recorded: true, travels: true }],
  ...overrides,
});

const listBody = (overrides: Record<string, unknown> = {}): unknown => ({
  ok: true,
  entries: [entry("entry-1")],
  total: 1,
  offset: 0,
  limit: 20,
  has_more: false,
  origins: ORIGINS,
  classes: CLASSES,
  warnings: [],
  warning_count: 0,
  ...overrides,
});

const previewBody = (overrides: Record<string, unknown> = {}): unknown => ({
  ok: true,
  message: "Restoring 1 path replaces no newer work.",
  preview_id: "preview-1",
  expires_at: "2026-01-02T03:09:05Z",
  entry_id: "entry-1",
  command: "bn web frontmatter set",
  origin: "browser",
  origin_label: "Browser",
  timestamp: "2026-01-02T03:04:05Z",
  requires_force: false,
  changes: [{
    path: "Source.md",
    current_state: "file(sha256=b,size=1)",
    restored_state: "file(sha256=a,size=1)",
    class: "note",
    recorded: true,
    travels: true,
    conflict: false,
  }],
  conflicts: [],
  ...overrides,
});

const contestedPreview = (): unknown => previewBody({
  message: "Restoring 1 path would replace newer work in Source.md.",
  requires_force: true,
  changes: [{
    path: "Source.md",
    current_state: "file(sha256=b,size=1)",
    restored_state: "file(sha256=a,size=1)",
    class: "note",
    recorded: true,
    travels: true,
    conflict: true,
  }],
  conflicts: [{
    path: "Source.md",
    expected_state: "file(sha256=a,size=1)",
    current_state: "file(sha256=b,size=1)",
  }],
});

type Call = { url: string; body: string };

const startWorkspace = (
  answer: (url: string, init?: RequestInit) => Response | Promise<Response>
): Call[] => {
  const calls: Call[] = [];
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    calls.push({ url, body: init?.body === undefined ? "" : String(init.body) });
    return answer(url, init);
  });
  initHistoryWorkspace({ fetch: fetchMock as typeof fetch });
  return calls;
};

const listOnly = (body: unknown) => (url: string): Response => {
  if (url.startsWith("/api/history?") || url === "/api/history") {
    return jsonResponse(body);
  }
  throw new Error(`unexpected request to ${url}`);
};

afterEach(() => {
  document.body.innerHTML = "";
  window.history.replaceState(null, "", "/history");
  vi.useRealTimers();
});

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
    expect(document.querySelector("[data-history-entry] .history-paths")?.textContent).toBe("Source.md");
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

const answerFor = (preview: unknown, undo: unknown, undoStatus = 200) =>
  (url: string): Response => {
    if (url === "/api/history/preview") {
      return jsonResponse(preview);
    }
    if (url === "/api/undo") {
      return jsonResponse(undo, undoStatus);
    }
    return jsonResponse(listBody());
  };

describe("history workspace restoring", () => {
  it("previews before it restores and sends no force when nothing is contested", async () => {
    renderWorkspace();
    const calls = startWorkspace(answerFor(previewBody(), {
      ok: true,
      message: "Undo restored 1 path.",
      entry_id: "entry-1",
    }));
    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-history-preview-entry]")).toHaveLength(1);
    });

    document.querySelector<HTMLButtonElement>("[data-history-preview-entry]")?.click();
    await waitForExpect(() => {
      expectReadyPreview();
    });
    expect(document.querySelector("[data-history-restore-facts]")?.textContent).toContain("Browser");
    expect(document.querySelector("[data-history-restore-changes]")?.textContent).toContain(
      "travels to your other replica"
    );
    expect(document.querySelector<HTMLElement>("[data-history-restore-conflicts]")?.hidden).toBe(true);
    const confirm = document.querySelector<HTMLButtonElement>("[data-history-restore-confirm]");
    expect(confirm?.disabled).toBe(false);
    expect(confirm?.textContent).toBe("Restore");

    confirm?.click();
    await waitForExpect(() => {
      expect(document.querySelector("[data-history-status]")?.textContent).toBe("Undo restored 1 path.");
    });
    expect(calls.map((call) => call.url)).toEqual([
      "/api/history?limit=20",
      "/api/history/preview",
      "/api/undo",
      "/api/history?limit=20",
    ]);
    expect(calls[2]?.body).toBe("token=csrf-token&id=entry-1&preview_id=preview-1");
    expect(document.querySelector<HTMLElement>("[data-history-restore]")?.hidden).toBe(true);
  });

  it("will not restore over newer work until the replacement is acknowledged", async () => {
    renderWorkspace();
    const calls = startWorkspace(answerFor(contestedPreview(), {
      ok: true,
      message: "Undo restored 1 path.",
      entry_id: "entry-1",
    }));
    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-history-preview-entry]")).toHaveLength(1);
    });

    document.querySelector<HTMLButtonElement>("[data-history-preview-entry]")?.click();
    await waitForExpect(() => {
      expect(document.querySelector<HTMLElement>("[data-history-restore-conflicts]")?.hidden).toBe(false);
    });
    expect(document.querySelector("[data-history-restore-conflict-list]")?.textContent).toContain(
      "Source.md: the history holds file(sha256=a,size=1), this vault now holds file(sha256=b,size=1)"
    );
    const confirm = document.querySelector<HTMLButtonElement>("[data-history-restore-confirm]");
    expect(confirm?.disabled).toBe(true);
    expect(confirm?.textContent).toBe("Restore and replace newer work");

    const consent = document.querySelector<HTMLInputElement>("[data-history-restore-consent]");
    if (!consent) {
      throw new Error("consent checkbox missing");
    }
    consent.checked = true;
    consent.dispatchEvent(new Event("change", { bubbles: true }));
    expect(confirm?.disabled).toBe(false);

    confirm?.click();
    await waitForExpect(() => {
      expect(document.querySelector("[data-history-status]")?.textContent).toBe("Undo restored 1 path.");
    });
    expect(calls[2]?.body).toBe(
      "token=csrf-token&id=entry-1&force=1&confirm_force=1&preview_id=preview-1"
    );
  });

});

describe("history workspace restore refusals", () => {
  it("closes the panel and says to look again when the receipt went stale", async () => {
    renderWorkspace();
    startWorkspace(answerFor(
      contestedPreview(),
      { ok: false, code: "preview_stale", message: "Source.md changed since the preview." },
      409
    ));
    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-history-preview-entry]")).toHaveLength(1);
    });

    document.querySelector<HTMLButtonElement>("[data-history-preview-entry]")?.click();
    await waitForExpect(() => {
      expect(document.querySelector<HTMLElement>("[data-history-restore-conflicts]")?.hidden).toBe(false);
    });
    const consent = document.querySelector<HTMLInputElement>("[data-history-restore-consent]");
    if (!consent) {
      throw new Error("consent checkbox missing");
    }
    consent.checked = true;
    consent.dispatchEvent(new Event("change", { bubbles: true }));
    document.querySelector<HTMLButtonElement>("[data-history-restore-confirm]")?.click();

    await waitForExpect(() => {
      expect(document.querySelector("[data-history-status]")?.textContent).toBe(
        "Source.md changed since the preview. Preview it again to see what changed."
      );
    });
    expect(document.querySelector<HTMLElement>("[data-history-restore]")?.hidden).toBe(true);
  });

  it("cancels without touching anything", async () => {
    renderWorkspace();
    const calls = startWorkspace(answerFor(previewBody(), { ok: true }));
    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-history-preview-entry]")).toHaveLength(1);
    });

    document.querySelector<HTMLButtonElement>("[data-history-preview-entry]")?.click();
    await waitForExpect(() => {
      expectReadyPreview();
    });
    document.querySelector<HTMLButtonElement>("[data-history-restore-cancel]")?.click();

    expect(document.querySelector<HTMLElement>("[data-history-restore]")?.hidden).toBe(true);
    expect(document.querySelector("[data-history-status]")?.textContent).toBe(
      "Restore canceled. Nothing was changed."
    );
    expect(calls.some((call) => call.url === "/api/undo")).toBe(false);
  });

  it("refuses to restore with no preview in hand", async () => {
    renderWorkspace();
    const calls = startWorkspace(listOnly(listBody()));
    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-history-preview-entry]")).toHaveLength(1);
    });

    document.querySelector<HTMLButtonElement>("[data-history-restore-confirm]")?.click();
    expect(document.querySelector("[data-history-status]")?.textContent).toBe(
      "Preview a change before restoring it."
    );
    expect(calls).toHaveLength(1);
  });

  // A restore error is not proof that nothing happened: a snapshot failure means
  // the files are already back and only the safety commit is missing, and a
  // failed rollback means content moved and could not be put back. Both leave a
  // vault the list on screen no longer describes.
  it("re-reads the history after a refused restore, not only after a successful one", async () => {
    renderWorkspace();
    const calls = startWorkspace(answerFor(
      previewBody(),
      { ok: false, code: "snapshot_failed", message: "The restore landed but could not be committed." },
      500
    ));
    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-history-preview-entry]")).toHaveLength(1);
    });

    document.querySelector<HTMLButtonElement>("[data-history-preview-entry]")?.click();
    await waitForExpect(() => {
      expectReadyPreview();
    });
    document.querySelector<HTMLButtonElement>("[data-history-restore-confirm]")?.click();

    await waitForExpect(() => {
      expect(calls.filter((call) => call.url.startsWith("/api/history?")).length).toBe(2);
    });
    // The refusal is still what the person is left reading — the re-read must not
    // overwrite it with "Showing 1 of 1 recorded changes."
    await waitForExpect(() => {
      expect(document.querySelector("[data-history-status]")?.textContent).toBe(
        "The restore landed but could not be committed."
      );
    });
  });
});

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

type ListAnswer = (body: unknown, status?: number) => void;

const statusText = (): string | null | undefined =>
  document.querySelector("[data-history-status]")?.textContent;

const statusClass = (): string =>
  document.querySelector("[data-history-status]")?.className || "";

const clickRefresh = (): void => {
  document.querySelector<HTMLButtonElement>("[data-history-refresh]")?.click();
};

/**
 * restoreWithDeferredReads drives the page through a confirmed restore and hands
 * back the list reads nobody has answered yet, so each test decides what the
 * read carrying the outcome does. Index 0 is the first paint, answered here;
 * index 1 is the re-read the restore itself started.
 */
const restoreWithDeferredReads = async (): Promise<ListAnswer[]> => {
  renderWorkspace();
  const pendingLists: ListAnswer[] = [];
  const fetchMock = vi.fn((input: RequestInfo | URL) => {
    const url = String(input);
    if (url === "/api/history/preview") {
      return Promise.resolve(jsonResponse(previewBody()));
    }
    if (url === "/api/undo") {
      return Promise.resolve(jsonResponse({
        ok: true,
        message: "Undo restored 1 path.",
        entry_id: "entry-1",
      }));
    }
    return new Promise<Response>((resolve) => {
      pendingLists.push((body, status = 200) => {
        resolve(jsonResponse(body, status));
      });
    });
  });
  initHistoryWorkspace({ fetch: fetchMock as unknown as typeof fetch });

  await waitForExpect(() => {
    expect(pendingLists).toHaveLength(1);
  });
  pendingLists[0](listBody());
  await waitForExpect(() => {
    expect(document.querySelectorAll("[data-history-preview-entry]")).toHaveLength(1);
  });
  document.querySelector<HTMLButtonElement>("[data-history-preview-entry]")?.click();
  await waitForExpect(() => {
    expectReadyPreview();
  });
  document.querySelector<HTMLButtonElement>("[data-history-restore-confirm]")?.click();
  await waitForExpect(() => {
    expect(pendingLists).toHaveLength(2);
  });
  return pendingLists;
};

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

const deferredPreviewWorkspace = async () => {
  renderWorkspace();
  const pending: Array<(body: unknown) => void> = [];
  const calls = startWorkspace((url) => {
    if (url === "/api/history/preview") {
      return new Promise<Response>((resolve) => pending.push((body) => resolve(jsonResponse(body))));
    }
    return jsonResponse(listBody({ entries: [entry("entry-1"), entry("entry-2")] }));
  });
  await waitForExpect(() => expect(document.querySelectorAll("[data-history-preview-entry]")).toHaveLength(2));
  const buttons = document.querySelectorAll<HTMLButtonElement>("[data-history-preview-entry]");
  return { pending, calls, buttons };
};

const settlePreview = async (resolve: (body: unknown) => void, body: unknown): Promise<void> => {
  resolve(body);
  await new Promise((done) => setTimeout(done, 0));
};

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
