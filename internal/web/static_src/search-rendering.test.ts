// @vitest-environment jsdom

import { afterEach, expect, it, vi } from "vitest";

import {
  initOverviewSearchResults,
  overviewSearchEmptyMessage,
  renderOverviewSearchResult,
} from "./overview-search";
import { renderSearchResultOption } from "./search";
import {
  renderOverviewBulkFixture,
  selectOverviewResult,
} from "./search-rendering.test-helpers";
import { searchResult, setURL } from "./search-test-helpers";

const trashPreviewFetch = () => vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
  if (String(input) === "/api/notes/bulk/preview") {
    expect(String(init!.body)).toContain("action=trash");
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

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  document.body.innerHTML = "";
  setURL("/");
});

it("renders primary autocomplete options with ARIA option state", () => {
  const activated: number[] = [];
  const item = renderSearchResultOption(
    document,
    searchResult({ snippet: "Body <mark>hit</mark>" }),
    2,
    {
      activate: (index) => activated.push(index),
    }
  );

  const button = item.querySelector("a");
  expect(button!.getAttribute("href")).toBe("/n/folder/note.md");
  expect(button!.id).toBe("search-option-2");
  expect(button!.getAttribute("role")).toBe("option");
  expect(button!.getAttribute("aria-selected")).toBe("false");
  expect(button!.getAttribute("data-search-index")).toBe("2");
  expect(button!.querySelector(".search-result-title")!.textContent).toBe("Note Title");
  expect(button!.querySelector(".search-result-folder")!.textContent).toBe("Folder: folder");
  expect(button!.querySelector(".search-result-snippet")!.innerHTML).toBe(
    "Body <mark>hit</mark>"
  );

  button!.dispatchEvent(new MouseEvent("mouseenter", { bubbles: true }));

  expect(activated).toEqual([2]);
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
  expect(list.querySelector("a")!.getAttribute("href")).toBe("/n/daily/overview.md");
  expect(list.querySelector(".overview-result-title")!.textContent).toBe("Note Title");
  expect(list.querySelector(".overview-result-meta")!.textContent).toContain(
    "Matched: title, body"
  );
  expect(list.querySelector(".overview-result-badge.is-issue")!.textContent).toBe("dead end");
  expect(list.querySelector(".overview-result-snippet")!.innerHTML).toBe(
    "<mark>overview</mark>"
  );
  expect(overviewSearchEmptyMessage("")).toBe("No notes match the active filters.");
  expect(overviewSearchEmptyMessage("zettel")).toBe('No notes match "zettel".');
});

it("renders a pin toggle on Library rows outside the result link", async () => {
  document.body.innerHTML = `
    <div data-overview-bulk data-overview-csrf="csrf-token"></div>
    <ul data-overview-search-list></ul>
  `;
  const list = document.querySelector<HTMLElement>("[data-overview-search-list]")!;
  const fetchMock = vi.fn(
    async () =>
      new Response(JSON.stringify({ ok: true, message: "Pinned note." }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      })
  );
  vi.stubGlobal("fetch", fetchMock);

  renderOverviewSearchResult(list, searchResult());
  const button = list.querySelector<HTMLButtonElement>(".pin-button");

  expect(button!.closest("a")).toBeNull();
  expect(button!.getAttribute("aria-pressed")).toBe("false");
  expect(button!.textContent).toBe("Pin");
  expect(button!.getAttribute("aria-label")).toBe("Pin Note Title");

  button!.click();
  expect(button!.getAttribute("aria-pressed")).toBe("true");

  await waitForExpect(() => {
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
  const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
  expect(url).toBe("/api/pins/set");
  expect(String(init.body)).toBe("key=folder%2Fnote.md&token=csrf-token");
});

it("renders a pinned Library row as pressed and reverts a refused unpin", async () => {
  document.body.innerHTML = `
    <div data-overview-bulk data-overview-csrf="csrf-token"></div>
    <ul data-overview-search-list></ul>
  `;
  const list = document.querySelector<HTMLElement>("[data-overview-search-list]")!;
  const fetchMock = vi.fn(
    async () =>
      new Response(JSON.stringify({ ok: false, code: "conflict", message: "note moved" }), {
        status: 409,
        headers: { "Content-Type": "application/json" },
      })
  );
  vi.stubGlobal("fetch", fetchMock);

  renderOverviewSearchResult(list, searchResult({ pinned: true }));
  const button = list.querySelector<HTMLButtonElement>(".pin-button");
  expect(button!.getAttribute("aria-pressed")).toBe("true");
  expect(button!.textContent).toBe("Unpin");

  button!.click();
  expect(button!.getAttribute("aria-pressed")).toBe("false");

  await waitForExpect(() => {
    expect(button!.getAttribute("aria-pressed")).toBe("true");
  });
  const [url] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
  expect(url).toBe("/api/pins/remove");
  expect(list.querySelector('[role="status"]')!.textContent).toBe("note moved");
  expect(list.querySelector<HTMLElement>(".overview-pin-feedback")!.hidden).toBe(false);

  fetchMock.mockImplementationOnce(async () => new Response(JSON.stringify({ ok: true })));
  const retry = list.querySelector<HTMLButtonElement>(".overview-pin-feedback button")!;
  retry.focus();
  retry.click();
  await waitForExpect(() => {
    expect(list.querySelector<HTMLElement>(".overview-pin-feedback")!.hidden).toBe(true);
  });
  expect(button!.getAttribute("aria-pressed")).toBe("false");
  expect(document.activeElement).toBe(button);
  expect(fetchMock).toHaveBeenCalledTimes(2);
});


it("renders trash preview details and applies from the preview panel", async () => {
  vi.useFakeTimers();
  setURL("/?q=source");
  renderOverviewBulkFixture();
  const fetchMock = trashPreviewFetch();
  const confirmMock = vi.fn(() => true);

  initOverviewSearchResults({ fetch: fetchMock, confirm: confirmMock });
  await vi.runAllTimersAsync();

  const bulkRoot = document.querySelector<HTMLElement>("[data-overview-bulk]");
  const selectedCount = document.querySelector<HTMLElement>("[data-overview-selected-count]");
  expect(bulkRoot!.hidden).toBe(true);

  selectOverviewResult();

  expect(bulkRoot!.hidden).toBe(false);
  expect(selectedCount!.textContent).toBe("1 selected");
  const trashButton = document.querySelector<HTMLButtonElement>("[data-bulk-trash]");
  expect(trashButton!.textContent).toBe("Preview Trash");

  trashButton!.click();
  await vi.runAllTimersAsync();

  expect(confirmMock).not.toHaveBeenCalled();
  expect(fetchMock.mock.calls.filter(([input]) => String(input) === "/api/notes/bulk")).toHaveLength(0);
  expect(trashButton!.textContent).toBe("Apply Trash");
  expect(bulkRoot!.getAttribute("data-overview-preview-id")).toBe("preview-trash-1");
  const previewPanel = document.querySelector<HTMLElement>("[data-overview-bulk-preview]");
  const previewTitle = document.querySelector<HTMLElement>("[data-overview-bulk-preview-title]");
  const previewCounts = document.querySelector<HTMLElement>("[data-overview-bulk-preview-counts]");
  const previewResults = document.querySelector<HTMLElement>("[data-overview-bulk-preview-results]");
  const previewFailures = document.querySelector<HTMLElement>("[data-overview-bulk-preview-failures]");
  expect(previewPanel!.hidden).toBe(false);
  expect(previewTitle!.textContent).toBe("Trash preview");
  expect(previewCounts!.textContent).toContain("Changed1");
  expect(previewCounts!.textContent).toContain("Failed1");
  expect(previewResults!.textContent).toContain("Source.md -> Trash/Source.md");
  expect(previewFailures!.textContent).toContain("missing: note not found");

  const previewApplyButton = document.querySelector<HTMLButtonElement>("[data-overview-bulk-preview-apply]");
  previewApplyButton!.click();
  await vi.runAllTimersAsync();

  const bulkCall = fetchMock.mock.calls.find(([input]) => String(input) === "/api/notes/bulk");
  expect(bulkCall).toBeTruthy();
  const init = bulkCall![1] as RequestInit;
  expect(init.method).toBe("POST");
  expect(String(init.body)).toContain("keys%5B%5D=source");
  expect(String(init.body)).toContain("action=trash");
  expect(String(init.body)).toContain("preview_id=preview-trash-1");
  expect(document.querySelector("[data-overview-bulk-status]")!.textContent).toContain(
    "Failures: missing: note not found."
  );
  expect(bulkRoot!.hidden).toBe(true);
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
  const moveInput = moveForm!.querySelector<HTMLInputElement>("input[name=\"folder\"]");
  moveInput!.value = "Archive";
  moveForm!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
  await vi.runAllTimersAsync();

  const previewPanel = document.querySelector<HTMLElement>("[data-overview-bulk-preview]");
  const previewResults = document.querySelector<HTMLElement>("[data-overview-bulk-preview-results]");
  expect(previewPanel!.hidden).toBe(false);
  expect(previewResults!.textContent).toContain("Source.md -> Archive/Source.md");
  expect(previewResults!.textContent).toContain("2 rewrites across 2 files");
  expect(previewResults!.textContent).toContain('status: "draft" -> "done"');

  const clearButton = document.querySelector<HTMLButtonElement>("[data-overview-bulk-preview-clear]");
  clearButton!.click();

  expect(previewPanel!.hidden).toBe(true);
  expect(document.querySelector<HTMLElement>("[data-overview-bulk]")!.hasAttribute("data-overview-preview-id")).toBe(false);
  expect(document.querySelector<HTMLButtonElement>("[data-bulk-move] button[type=\"submit\"]")!.textContent).toBe("Preview Move");
});

it("clears a move preview when the form changes and requires a fresh preview", async () => {
  vi.useFakeTimers();
  setURL("/?q=source");
  renderOverviewBulkFixture();
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    if (String(input) === "/api/notes/bulk/preview") {
      const body = String(init!.body);
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
  const moveInput = moveForm!.querySelector<HTMLInputElement>("input[name=\"folder\"]");
  const moveButton = moveForm!.querySelector<HTMLButtonElement>("button[type=\"submit\"]");
  expect(moveInput).not.toBeNull();

  moveInput!.value = "Archive";
  moveForm!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
  await vi.runAllTimersAsync();

  expect(moveButton!.textContent).toBe("Apply Move");

  moveInput!.value = "Later";
  moveInput!.dispatchEvent(new Event("input", { bubbles: true }));
  expect(moveButton!.textContent).toBe("Preview Move");

  moveForm!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
  await vi.runAllTimersAsync();

  const previewCalls = fetchMock.mock.calls.filter(([input]) => String(input) === "/api/notes/bulk/preview");
  expect(previewCalls).toHaveLength(2);
  expect(fetchMock.mock.calls.filter(([input]) => String(input) === "/api/notes/bulk")).toHaveLength(0);
  expect(String(previewCalls[1]![1]!.body)).toContain("folder=Later");
  expect(moveButton!.textContent).toBe("Apply Move");
});
