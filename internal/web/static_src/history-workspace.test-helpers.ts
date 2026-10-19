import { expect, vi } from "vitest";
import { initHistoryWorkspace } from "./history-workspace";
import { jsonResponse, waitForExpect as sharedWaitForExpect } from "./shared.test-helpers";

export const waitForExpect = (assertion: () => void): Promise<void> =>
  sharedWaitForExpect(assertion, { attempts: 40 });

// The markup mirrors internal/web/templates/history.html. The page test in
// internal/web/history_page_test.go is what holds the two in step: it names
// every attribute this file reads, so a template that stopped emitting one
// fails there rather than leaving this suite testing a page nobody serves.
export const renderWorkspace = (): void => {
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

export const expectReadyPreview = (): void => {
  expect(document.querySelector<HTMLElement>("[data-history-restore]")?.hidden).toBe(false);
  expect(document.querySelector("[data-history-restore]")?.getAttribute("aria-busy")).toBe("false");
};

export const ORIGINS = [
  { value: "browser", label: "Browser" },
  { value: "import", label: "Imported from your other replica" },
];

export const CLASSES = [
  { value: "note", label: "Notes" },
  { value: "attachment", label: "Attachments" },
];

export const entry = (id: string, overrides: Record<string, unknown> = {}): unknown => ({
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

export const listBody = (overrides: Record<string, unknown> = {}): unknown => ({
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

export const previewBody = (overrides: Record<string, unknown> = {}): unknown => ({
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

export const contestedPreview = (): unknown => previewBody({
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

export type Call = { url: string; body: string };

export const startWorkspace = (
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

export const listOnly = (body: unknown) => (url: string): Response => {
  if (url.startsWith("/api/history?") || url === "/api/history") {
    return jsonResponse(body);
  }
  throw new Error(`unexpected request to ${url}`);
};

export const answerFor = (preview: unknown, undo: unknown, undoStatus = 200) =>
  (url: string): Response => {
    if (url === "/api/history/preview") {
      return jsonResponse(preview);
    }
    if (url === "/api/undo") {
      return jsonResponse(undo, undoStatus);
    }
    return jsonResponse(listBody());
  };

export type ListAnswer = (body: unknown, status?: number) => void;

export const statusText = (): string | null | undefined =>
  document.querySelector("[data-history-status]")?.textContent;

export const statusClass = (): string =>
  document.querySelector("[data-history-status]")?.className || "";

export const clickRefresh = (): void => {
  document.querySelector<HTMLButtonElement>("[data-history-refresh]")?.click();
};

/**
 * restoreWithDeferredReads drives the page through a confirmed restore and hands
 * back the list reads nobody has answered yet, so each test decides what the
 * read carrying the outcome does. Index 0 is the first paint, answered here;
 * index 1 is the re-read the restore itself started.
 */
export const restoreWithDeferredReads = async (): Promise<ListAnswer[]> => {
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

export const deferredPreviewWorkspace = async () => {
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

export const settlePreview = async (resolve: (body: unknown) => void, body: unknown): Promise<void> => {
  resolve(body);
  await new Promise((done) => setTimeout(done, 0));
};

export const cleanup = (): void => {
  document.body.innerHTML = "";
  window.history.replaceState(null, "", "/history");
  vi.useRealTimers();
};

export { jsonResponse };
