// @vitest-environment jsdom

import { afterEach, expect, it, vi } from "vitest";

import { dispatchFiltersChange } from "./filters";
import { initOverviewSearchResults } from "./overview-search";
import {
  renderOverviewBulkFixture,
  selectOverviewResult,
} from "./search-rendering.test-helpers";
import { searchResult, setURL } from "./search-test-helpers";

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  document.body.innerHTML = "";
  setURL("/");
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

    selectOverviewResult();
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
