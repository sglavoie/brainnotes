// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import {
  buildBulkApplyForm,
  buildBulkNotesForm,
  buildBulkPreviewForm,
  normalizeBulkFrontmatterChanges,
  normalizeBulkNoteKeys,
  normalizeBulkManagementResult,
  normalizeBulkPreviewManagementResult,
  normalizeBulkPreviewValue,
  normalizeManagementErrorMessage,
  normalizeSearchResults,
  searchResultPath,
  type SearchResult,
} from "./api";
import { buildSearchAPIURL, dispatchFiltersChange } from "./filters";
import {
  initOverviewSearchResults,
  overviewSearchEmptyMessage,
  renderOverviewSearchResult,
} from "./overview-search";
import {
  initSearch,
  nextSearchActiveIndex,
  renderSearchResultOption,
} from "./search";

const searchResult = (overrides: Partial<SearchResult> = {}): SearchResult => ({
  key: "folder/note.md",
  path: "/n/folder/note.md",
  title: "Note Title",
  folder: "folder",
  matched_fields: ["title"],
  issues: [],
  snippet: "Snippet text",
  ...overrides,
});

const setURL = (url: string): void => {
  window.history.replaceState(null, "", url);
};

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  document.body.innerHTML = "";
  setURL("/");
});

describe("search URL and result contracts", () => {
  it("builds search API URLs with optional shared filters", () => {
    setURL("/?tag=projects&category=weekly&q=old&broken=true#top");

    expect(buildSearchAPIURL(" daily note ", true)).toBe(
      "/api/search?q=daily+note&tag=projects&category=weekly&broken=true"
    );
    expect(buildSearchAPIURL(" daily note ", false)).toBe("/api/search?q=daily+note");
    expect(buildSearchAPIURL("", false)).toBe("/api/search");
  });

  it("normalizes result payloads and resolves safe note paths", () => {
    const results = normalizeSearchResults([
      {
        key: " daily/index.md ",
        path: " /n/server/path ",
        title: " Daily Index ",
        folder: " daily ",
        matched_fields: [" title ", "", "body"],
        issues: ["dead_end", 3],
        snippet: " <mark>daily</mark> ",
      },
      {
        key: "folder/name with spaces.md",
      },
      "bad",
    ]);

    expect(results).toHaveLength(2);
    expect(results[0]).toEqual({
      key: "daily/index.md",
      path: "/n/server/path",
      title: "Daily Index",
      folder: "daily",
      matched_fields: ["title", "body"],
      issues: ["dead_end", "3"],
      snippet: "<mark>daily</mark>",
    });
    expect(searchResultPath(results[0])).toBe("/n/server/path");
    expect(searchResultPath(results[1])).toBe("/n/folder/name%20with%20spaces.md");
  });

  it("normalizes explicit selected keys for bulk preview and apply requests", () => {
    expect(normalizeBulkNoteKeys([" source ", "", "Source", "folder/Note"])).toEqual([
      "source",
      "folder/Note",
    ]);

    const previewBody = buildBulkPreviewForm({
      action: "move",
      keys: [" source ", "Source", "archive/a"],
      token: "csrf-token",
      folder: "Archive",
    });
    const applyBody = buildBulkApplyForm({
      action: "frontmatter_set",
      keys: [" source ", "Source", "archive/a"],
      token: "csrf-token",
      property: "status",
      op: "=",
      value: "done",
      previewID: " preview-123 ",
    });
    const compatBody = buildBulkNotesForm({
      action: "frontmatter_unset",
      keys: [" source ", "Source"],
      token: "csrf-token",
      property: "status",
      previewID: "preview-compat",
    });

    expect(previewBody.get("action")).toBe("move");
    expect(previewBody.get("token")).toBe("csrf-token");
    expect(previewBody.get("folder")).toBe("Archive");
    expect(previewBody.get("preview_id")).toBeNull();
    expect(previewBody.getAll("keys[]")).toEqual(["source", "archive/a"]);

    expect(applyBody.get("action")).toBe("frontmatter_set");
    expect(applyBody.get("property")).toBe("status");
    expect(applyBody.get("op")).toBe("=");
    expect(applyBody.get("value")).toBe("done");
    expect(applyBody.get("preview_id")).toBe("preview-123");
    expect(applyBody.getAll("keys[]")).toEqual(["source", "archive/a"]);

    expect(compatBody.get("preview_id")).toBe("preview-compat");
  });

  it("normalizes bulk preview rows, frontmatter changes, failures, and expiry timestamps", () => {
    const payload = normalizeBulkPreviewManagementResult({
      ok: true,
      action: " move ",
      message: " Preview: 1 note would be moved. 1 failed. ",
      changed: "1",
      unchanged: "0",
      failed: "1",
      preview_id: " preview-123 ",
      expires_at: "2026-06-02T12:34:56-04:00",
      results: [
        {
          key: " inbox/source ",
          path: " /n/archive/source ",
          rel_path: " Archive/Source.md ",
          source: {
            key: " inbox/source ",
            path: " /n/inbox/source ",
            rel_path: " Inbox/Source.md ",
          },
          destination: {
            key: " archive/source ",
            path: " /n/archive/source ",
            rel_path: " Archive/Source.md ",
          },
          changed: true,
          move: {
            rewrite_count: "2",
            rewrite_file_count: "2",
            body_rewrite_count: "1",
            frontmatter_rewrite_count: "1",
          },
          frontmatter: [
            {
              key: "status",
              before: { present: true, value: "draft" },
              after: { present: true, value: "done" },
              changed: 1,
            },
            {
              key: "empty_value",
              before: { present: true, value: "" },
              after: { present: false, value: "ignored" },
              changed: true,
            },
          ],
          affected: [
            {
              role: " source ",
              key: " inbox/source ",
              path: " /n/inbox/source ",
              rel_path: " Inbox/Source.md ",
              exists: 1,
            },
            {
              role: " destination ",
              key: " archive/source ",
              path: " /n/archive/source ",
              rel_path: " Archive/Source.md ",
              exists: false,
            },
          ],
        },
        "bad",
      ],
      failures: [
        { key: " missing ", rel_path: " Missing.md ", code: " not_found ", message: " note not found " },
        {},
      ],
    });

    expect(payload).toEqual({
      ok: true,
      action: "move",
      message: "Preview: 1 note would be moved. 1 failed.",
      changed: 1,
      unchanged: 0,
      failed: 1,
      preview_id: "preview-123",
      expires_at: "2026-06-02T16:34:56.000Z",
      results: [
        {
          key: "inbox/source",
          path: "/n/archive/source",
          rel_path: "Archive/Source.md",
          source: {
            key: "inbox/source",
            path: "/n/inbox/source",
            rel_path: "Inbox/Source.md",
          },
          destination: {
            key: "archive/source",
            path: "/n/archive/source",
            rel_path: "Archive/Source.md",
          },
          changed: true,
          move: {
            rewrite_count: 2,
            rewrite_file_count: 2,
            body_rewrite_count: 1,
            frontmatter_rewrite_count: 1,
          },
          frontmatter: [
            {
              key: "status",
              before: { present: true, value: "draft" },
              after: { present: true, value: "done" },
              changed: true,
            },
            {
              key: "empty_value",
              before: { present: true, value: "" },
              after: { present: false },
              changed: true,
            },
          ],
          affected: [
            {
              role: "source",
              key: "inbox/source",
              path: "/n/inbox/source",
              rel_path: "Inbox/Source.md",
              exists: true,
            },
            {
              role: "destination",
              key: "archive/source",
              path: "/n/archive/source",
              rel_path: "Archive/Source.md",
              exists: false,
            },
          ],
        },
      ],
      failures: [
        {
          key: "missing",
          rel_path: "Missing.md",
          code: "not_found",
          message: "note not found",
        },
      ],
    });

    expect(normalizeBulkPreviewValue({ present: false, value: "ignored" })).toEqual({
      present: false,
    });
    expect(normalizeBulkFrontmatterChanges([
      { key: "status", before: { present: false }, after: { present: true, value: "done" }, changed: "true" },
    ])).toEqual([
      {
        key: "status",
        before: { present: false },
        after: { present: true, value: "done" },
        changed: true,
      },
    ]);
  });

  it("normalizes bulk apply results and preserves preview error messages", () => {
    expect(normalizeBulkManagementResult({
      ok: true,
      message: " Updated frontmatter on 1 note. ",
      changed: "1",
      unchanged: "2",
      failed: "1",
      journal_id: " journal-123 ",
      results: [
        { key: " source ", path: " /n/source ", rel_path: " Source.md ", changed: 1 },
      ],
      failures: [
        { key: " missing ", code: " not_found ", message: " note not found " },
      ],
    })).toEqual({
      ok: true,
      message: "Updated frontmatter on 1 note.",
      changed: 1,
      unchanged: 2,
      failed: 1,
      journal_id: "journal-123",
      results: [
        {
          key: "source",
          path: "/n/source",
          rel_path: "Source.md",
          changed: true,
        },
      ],
      failures: [
        {
          key: "missing",
          code: "not_found",
          message: "note not found",
        },
      ],
    });

    expect(normalizeManagementErrorMessage(409, {
      code: "preview_required",
      message: " bulk preview required ",
    })).toBe("bulk preview required");
    expect(normalizeManagementErrorMessage(409, {
      code: "preview_stale",
      message: " bulk preview is stale ",
    })).toBe("bulk preview is stale");
    expect(normalizeManagementErrorMessage(503, { code: "preview_stale" })).toBe(
      "Request failed (503)."
    );
  });
});

describe("search result rendering", () => {
  it("renders primary autocomplete options with ARIA option state", () => {
    const activated: number[] = [];
    const chosen: number[] = [];
    const item = renderSearchResultOption(
      document,
      searchResult({ snippet: "Body <mark>hit</mark>" }),
      2,
      {
        activate: (index) => activated.push(index),
        choose: (index) => chosen.push(index),
      }
    );

    const button = item.querySelector("button");
    expect(button?.id).toBe("search-option-2");
    expect(button?.getAttribute("role")).toBe("option");
    expect(button?.getAttribute("aria-selected")).toBe("false");
    expect(button?.getAttribute("data-search-index")).toBe("2");
    expect(button?.querySelector(".search-result-title")?.textContent).toBe("Note Title");
    expect(button?.querySelector(".search-result-folder")?.textContent).toBe("Folder: folder");
    expect(button?.querySelector(".search-result-snippet")?.innerHTML).toBe(
      "Body <mark>hit</mark>"
    );

    button?.dispatchEvent(new MouseEvent("mouseenter", { bubbles: true }));
    button?.dispatchEvent(new MouseEvent("click", { bubbles: true }));

    expect(activated).toEqual([2]);
    expect(chosen).toEqual([2]);
  });

  it("renders overview results with metadata, issue badges, and fallback paths", () => {
    const list = document.createElement("ul");
    const rendered = renderOverviewSearchResult(
      list,
      searchResult({
        path: "",
        key: "daily/overview.md",
        matched_fields: ["title", "body"],
        issues: ["dead_end"],
        snippet: "<mark>overview</mark>",
      })
    );

    expect(rendered).toBe(true);
    expect(list.querySelector("a")?.getAttribute("href")).toBe("/n/daily/overview.md");
    expect(list.querySelector(".overview-result-title")?.textContent).toBe("Note Title");
    expect(list.querySelector(".overview-result-meta")?.textContent).toContain(
      "Matched: title, body"
    );
    expect(list.querySelector(".overview-result-badge.is-issue")?.textContent).toBe("dead end");
    expect(list.querySelector(".overview-result-snippet")?.innerHTML).toBe(
      "<mark>overview</mark>"
    );
    expect(overviewSearchEmptyMessage("")).toBe("No notes match the active filters.");
    expect(overviewSearchEmptyMessage("zettel")).toBe('No notes match "zettel".');
  });

  const renderOverviewBulkFixture = (): void => {
    document.body.innerHTML = `
      <input id="search-input" />
      <section data-overview-search-results aria-busy="false">
        <p data-overview-search-status></p>
        <div data-overview-bulk data-overview-csrf="csrf-token" hidden>
          <p data-overview-selected-count></p>
          <button type="button" data-bulk-trash>Trash</button>
          <form data-bulk-move>
            <input name="folder" type="text" />
            <button type="submit">Move</button>
          </form>
          <form data-bulk-frontmatter-set>
            <input name="property" type="text" />
            <select name="op">
              <option value="=">=</option>
              <option value="+=">+=</option>
              <option value="-=">-=</option>
            </select>
            <input name="value" type="text" />
            <button type="submit">Apply</button>
          </form>
          <form data-bulk-frontmatter-unset>
            <input name="property" type="text" />
            <button type="submit">Unset</button>
          </form>
          <section data-overview-bulk-preview hidden>
            <h3 data-overview-bulk-preview-title>Bulk preview</h3>
            <p data-overview-bulk-preview-message></p>
            <button type="button" data-overview-bulk-preview-apply>Apply</button>
            <button type="button" data-overview-bulk-preview-clear>Clear</button>
            <dl data-overview-bulk-preview-counts></dl>
            <ul data-overview-bulk-preview-results></ul>
            <ul data-overview-bulk-preview-failures></ul>
          </section>
          <p data-overview-bulk-status></p>
        </div>
        <ul data-overview-search-list hidden></ul>
      </section>
    `;
  };

  const selectOverviewResult = (): HTMLInputElement => {
    const checkbox = document.querySelector<HTMLInputElement>(".overview-result-select input");
    expect(checkbox).not.toBeNull();
    checkbox!.checked = true;
    checkbox!.dispatchEvent(new Event("change", { bubbles: true }));
    return checkbox!;
  };

  it("renders trash preview details and applies from the preview panel", async () => {
    vi.useFakeTimers();
    setURL("/?q=source");
    renderOverviewBulkFixture();
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input) === "/api/notes/bulk/preview") {
        expect(String(init?.body)).toContain("action=trash");
        return new Response(JSON.stringify({
          ok: true,
          action: "trash",
          message: "Preview: 1 note would be moved to Trash. 1 failed.",
          changed: 1,
          failed: 1,
          preview_id: "preview-trash-1",
          expires_at: "2026-06-02T16:34:56Z",
          results: [
            {
              key: "source",
              path: "/n/trash/source",
              rel_path: "Trash/Source.md",
              source: {
                key: "source",
                path: "/n/source",
                rel_path: "Source.md",
              },
              destination: {
                key: "trash/source",
                path: "/n/trash/source",
                rel_path: "Trash/Source.md",
              },
              changed: true,
            },
          ],
          failures: [{ key: "missing", message: "note not found" }],
        }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      if (String(input) === "/api/notes/bulk") {
        return new Response(JSON.stringify({
          ok: true,
          message: "Moved 1 note to Trash. 1 failed.",
          changed: 1,
          failed: 1,
          failures: [{ key: "missing", message: "note not found" }],
        }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      return new Response(JSON.stringify([searchResult({
        key: "source",
        path: "/n/source",
        title: "Source",
      })]), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    });
    const confirmMock = vi.fn(() => true);

    initOverviewSearchResults({ fetch: fetchMock, confirm: confirmMock });
    await vi.runAllTimersAsync();

    const bulkRoot = document.querySelector<HTMLElement>("[data-overview-bulk]");
    const selectedCount = document.querySelector<HTMLElement>("[data-overview-selected-count]");
    expect(bulkRoot?.hidden).toBe(true);

    selectOverviewResult();

    expect(bulkRoot?.hidden).toBe(false);
    expect(selectedCount?.textContent).toBe("1 selected");
    const trashButton = document.querySelector<HTMLButtonElement>("[data-bulk-trash]");
    expect(trashButton?.textContent).toBe("Preview Trash");

    trashButton?.click();
    await vi.runAllTimersAsync();

    expect(confirmMock).not.toHaveBeenCalled();
    expect(fetchMock.mock.calls.filter(([input]) => String(input) === "/api/notes/bulk")).toHaveLength(0);
    expect(trashButton?.textContent).toBe("Apply Trash");
    expect(bulkRoot?.getAttribute("data-overview-preview-id")).toBe("preview-trash-1");
    const previewPanel = document.querySelector<HTMLElement>("[data-overview-bulk-preview]");
    const previewTitle = document.querySelector<HTMLElement>("[data-overview-bulk-preview-title]");
    const previewCounts = document.querySelector<HTMLElement>("[data-overview-bulk-preview-counts]");
    const previewResults = document.querySelector<HTMLElement>("[data-overview-bulk-preview-results]");
    const previewFailures = document.querySelector<HTMLElement>("[data-overview-bulk-preview-failures]");
    expect(previewPanel?.hidden).toBe(false);
    expect(previewTitle?.textContent).toBe("Trash preview");
    expect(previewCounts?.textContent).toContain("Changed1");
    expect(previewCounts?.textContent).toContain("Failed1");
    expect(previewResults?.textContent).toContain("Source.md -> Trash/Source.md");
    expect(previewFailures?.textContent).toContain("missing: note not found");

    const previewApplyButton = document.querySelector<HTMLButtonElement>("[data-overview-bulk-preview-apply]");
    previewApplyButton?.click();
    await vi.runAllTimersAsync();

    const bulkCall = fetchMock.mock.calls.find(([input]) => String(input) === "/api/notes/bulk");
    expect(bulkCall).toBeTruthy();
    const init = bulkCall?.[1] as RequestInit;
    expect(init.method).toBe("POST");
    expect(String(init.body)).toContain("keys%5B%5D=source");
    expect(String(init.body)).toContain("action=trash");
    expect(String(init.body)).toContain("preview_id=preview-trash-1");
    expect(document.querySelector("[data-overview-bulk-status]")?.textContent).toContain(
      "Failures: missing: note not found."
    );
    expect(bulkRoot?.hidden).toBe(true);
  });

  it("renders move and frontmatter preview details and clears the panel", async () => {
    vi.useFakeTimers();
    setURL("/?q=source");
    renderOverviewBulkFixture();
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      if (String(input) === "/api/notes/bulk/preview") {
        return new Response(JSON.stringify({
          ok: true,
          action: "move",
          message: "Preview: 1 note would be moved.",
          changed: 1,
          preview_id: "preview-move-1",
          expires_at: "2026-06-02T16:34:56Z",
          results: [
            {
              key: "source",
              path: "/n/archive/source",
              rel_path: "Archive/Source.md",
              source: {
                key: "source",
                path: "/n/source",
                rel_path: "Source.md",
              },
              destination: {
                key: "archive/source",
                path: "/n/archive/source",
                rel_path: "Archive/Source.md",
              },
              changed: true,
              move: {
                rewrite_count: 2,
                rewrite_file_count: 2,
                body_rewrite_count: 1,
                frontmatter_rewrite_count: 1,
              },
              frontmatter: [
                {
                  key: "status",
                  before: { present: true, value: "draft" },
                  after: { present: true, value: "done" },
                  changed: true,
                },
              ],
            },
          ],
        }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      return new Response(JSON.stringify([searchResult({
        key: "source",
        path: "/n/source",
        title: "Source",
      })]), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    });

    initOverviewSearchResults({ fetch: fetchMock });
    await vi.runAllTimersAsync();
    selectOverviewResult();

    const moveForm = document.querySelector<HTMLFormElement>("[data-bulk-move]");
    const moveInput = moveForm?.querySelector<HTMLInputElement>("input[name=\"folder\"]");
    moveInput!.value = "Archive";
    moveForm?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await vi.runAllTimersAsync();

    const previewPanel = document.querySelector<HTMLElement>("[data-overview-bulk-preview]");
    const previewResults = document.querySelector<HTMLElement>("[data-overview-bulk-preview-results]");
    expect(previewPanel?.hidden).toBe(false);
    expect(previewResults?.textContent).toContain("Source.md -> Archive/Source.md");
    expect(previewResults?.textContent).toContain("2 rewrites across 2 files");
    expect(previewResults?.textContent).toContain('status: "draft" -> "done"');

    const clearButton = document.querySelector<HTMLButtonElement>("[data-overview-bulk-preview-clear]");
    clearButton?.click();

    expect(previewPanel?.hidden).toBe(true);
    expect(document.querySelector<HTMLElement>("[data-overview-bulk]")?.hasAttribute("data-overview-preview-id")).toBe(false);
    expect(document.querySelector<HTMLButtonElement>("[data-bulk-move] button[type=\"submit\"]")?.textContent).toBe("Preview Move");
  });

  it("clears a move preview when the form changes and requires a fresh preview", async () => {
    vi.useFakeTimers();
    setURL("/?q=source");
    renderOverviewBulkFixture();
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input) === "/api/notes/bulk/preview") {
        const body = String(init?.body);
        const previewID = body.includes("folder=Later") ? "preview-move-2" : "preview-move-1";
        return new Response(JSON.stringify({
          ok: true,
          action: "move",
          message: "Preview: 1 note would be moved.",
          changed: 1,
          preview_id: previewID,
          expires_at: "2026-06-02T16:34:56Z",
        }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      if (String(input) === "/api/notes/bulk") {
        return new Response(JSON.stringify({
          ok: true,
          message: "Moved 1 note.",
          changed: 1,
        }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      return new Response(JSON.stringify([searchResult({
        key: "source",
        path: "/n/source",
        title: "Source",
      })]), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    });

    initOverviewSearchResults({ fetch: fetchMock });
    await vi.runAllTimersAsync();
    selectOverviewResult();

    const moveForm = document.querySelector<HTMLFormElement>("[data-bulk-move]");
    const moveInput = moveForm?.querySelector<HTMLInputElement>("input[name=\"folder\"]");
    const moveButton = moveForm?.querySelector<HTMLButtonElement>("button[type=\"submit\"]");
    expect(moveInput).not.toBeNull();

    moveInput!.value = "Archive";
    moveForm?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await vi.runAllTimersAsync();

    expect(moveButton?.textContent).toBe("Apply Move");

    moveInput!.value = "Later";
    moveInput!.dispatchEvent(new Event("input", { bubbles: true }));
    expect(moveButton?.textContent).toBe("Preview Move");

    moveForm?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await vi.runAllTimersAsync();

    const previewCalls = fetchMock.mock.calls.filter(([input]) => String(input) === "/api/notes/bulk/preview");
    expect(previewCalls).toHaveLength(2);
    expect(fetchMock.mock.calls.filter(([input]) => String(input) === "/api/notes/bulk")).toHaveLength(0);
    expect(String(previewCalls[1]?.[1]?.body)).toContain("folder=Later");
    expect(moveButton?.textContent).toBe("Apply Move");
  });

  it("clears preview state when search filters refresh", async () => {
    vi.useFakeTimers();
    setURL("/?q=source");
    renderOverviewBulkFixture();
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      if (String(input) === "/api/notes/bulk/preview") {
        return new Response(JSON.stringify({
          ok: true,
          action: "trash",
          message: "Preview: 1 note would be moved to Trash.",
          changed: 1,
          preview_id: "preview-trash-refresh",
          expires_at: "2026-06-02T16:34:56Z",
        }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      return new Response(JSON.stringify([searchResult({
        key: "source",
        path: "/n/source",
        title: "Source",
      })]), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    });

    initOverviewSearchResults({ fetch: fetchMock });
    await vi.runAllTimersAsync();
    selectOverviewResult();

    const trashButton = document.querySelector<HTMLButtonElement>("[data-bulk-trash]");
    const bulkRoot = document.querySelector<HTMLElement>("[data-overview-bulk]");
    trashButton?.click();
    await vi.runAllTimersAsync();

    expect(trashButton?.textContent).toBe("Apply Trash");
    setURL("/?q=source&tag=work");
    dispatchFiltersChange(window, {
      mode: "search",
      params: new URLSearchParams("tag=work"),
    });
    await vi.runAllTimersAsync();

    expect(trashButton?.textContent).toBe("Preview Trash");
    expect(bulkRoot?.hasAttribute("data-overview-preview-id")).toBe(false);

    trashButton?.click();
    await vi.runAllTimersAsync();

    expect(fetchMock.mock.calls.filter(([input]) => String(input) === "/api/notes/bulk/preview")).toHaveLength(2);
    expect(fetchMock.mock.calls.filter(([input]) => String(input) === "/api/notes/bulk")).toHaveLength(0);
  });

  it("clears stale preview ids after an apply conflict", async () => {
    vi.useFakeTimers();
    setURL("/?q=source");
    renderOverviewBulkFixture();
    let applyCalls = 0;
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      if (String(input) === "/api/notes/bulk/preview") {
        return new Response(JSON.stringify({
          ok: true,
          action: "trash",
          message: "Preview: 1 note would be moved to Trash.",
          changed: 1,
          preview_id: "preview-trash-stale",
          expires_at: "2026-06-02T16:34:56Z",
        }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      if (String(input) === "/api/notes/bulk") {
        applyCalls += 1;
        return new Response(JSON.stringify({
          ok: false,
          code: "preview_stale",
          message: "bulk preview is stale",
        }), {
          status: 409,
          headers: { "Content-Type": "application/json" },
        });
      }
      return new Response(JSON.stringify([searchResult({
        key: "source",
        path: "/n/source",
        title: "Source",
      })]), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    });

    initOverviewSearchResults({ fetch: fetchMock });
    await vi.runAllTimersAsync();
    selectOverviewResult();

    const trashButton = document.querySelector<HTMLButtonElement>("[data-bulk-trash]");
    trashButton?.click();
    await vi.runAllTimersAsync();
    expect(trashButton?.textContent).toBe("Apply Trash");

    trashButton?.click();
    await vi.runAllTimersAsync();

    expect(applyCalls).toBe(1);
    expect(document.querySelector("[data-overview-bulk-status]")?.textContent).toBe("bulk preview is stale");
    expect(trashButton?.textContent).toBe("Preview Trash");

    trashButton?.click();
    await vi.runAllTimersAsync();
    expect(fetchMock.mock.calls.filter(([input]) => String(input) === "/api/notes/bulk/preview")).toHaveLength(2);
  });
});

describe("search keyboard and filter-change behavior", () => {
  it("wraps active search result indexes for keyboard navigation", () => {
    expect(nextSearchActiveIndex(-1, 3, "next")).toBe(0);
    expect(nextSearchActiveIndex(0, 3, "next")).toBe(1);
    expect(nextSearchActiveIndex(2, 3, "next")).toBe(0);
    expect(nextSearchActiveIndex(-1, 3, "previous")).toBe(2);
    expect(nextSearchActiveIndex(0, 3, "previous")).toBe(2);
    expect(nextSearchActiveIndex(1, 3, "previous")).toBe(0);
    expect(nextSearchActiveIndex(0, 0, "next")).toBe(-1);
  });

  it("runs a filter-aware search when search-mode filters change", async () => {
    vi.useFakeTimers();
    setURL("/");
    document.body.innerHTML = `
      <section data-search-box>
        <input id="search-input" aria-expanded="false" />
        <ul id="search-results" hidden></ul>
      </section>
      <div data-facet-controls data-facet-mode="search"></div>
    `;
    const payload = [
      searchResult({ title: "Filtered A", path: "/n/filtered/a.md" }),
      searchResult({ title: "Filtered B", path: "/n/filtered/b.md" }),
    ];
    const fetchMock = vi.fn(async () => ({
      ok: true,
      json: async () => payload,
    })) as unknown as typeof fetch;
    vi.stubGlobal("fetch", fetchMock);

    initSearch();
    setURL("/?tag=work");
    dispatchFiltersChange(window, {
      mode: "search",
      params: new URLSearchParams("tag=work"),
    });

    await vi.runAllTimersAsync();

    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock).toHaveBeenCalledWith("/api/search?tag=work", {
      headers: { Accept: "application/json" },
    });
    const input = document.getElementById("search-input") as HTMLInputElement;
    const resultsEl = document.getElementById("search-results") as HTMLElement;
    expect(resultsEl.hidden).toBe(false);
    expect(input.getAttribute("aria-expanded")).toBe("true");
    expect(resultsEl.querySelectorAll(".search-result")).toHaveLength(2);

    input.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true }));
    const firstOption = document.getElementById("search-option-0");
    expect(firstOption?.getAttribute("aria-selected")).toBe("true");
    expect(firstOption?.classList.contains("is-active")).toBe(true);
    expect(input.getAttribute("aria-activedescendant")).toBe("search-option-0");

    input.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    expect(resultsEl.hidden).toBe(true);
    expect(input.getAttribute("aria-expanded")).toBe("false");
    expect(input.hasAttribute("aria-activedescendant")).toBe(false);
  });
});
