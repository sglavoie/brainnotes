// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { cancelGraphLoad, scheduleLoad } from "./graph-controller-load";
import { createGraphRuntime } from "./graph-controller-runtime";
import { setStatus } from "./graph-controller-status";
import { initOverviewSearchResults } from "./overview-search";
import { renderOverviewBulkFixture } from "./search-rendering.test-helpers";
import { searchResult, setURL } from "./search-test-helpers";

afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); document.body.innerHTML = ""; setURL("/"); });

it("clears Library busy state when IME composition cancels an in-flight search", async () => {
  vi.useFakeTimers();
  renderOverviewBulkFixture();
  setURL("/library?q=old");
  const fetcher = vi.fn((): Promise<Response> => fetcher.mock.calls.length === 1
    ? Promise.resolve(new Response(JSON.stringify([searchResult({ title: "Old" })])))
    : new Promise(() => {}));
  initOverviewSearchResults({ fetch: fetcher });
  await vi.advanceTimersByTimeAsync(1);
  const root = document.querySelector<HTMLElement>("[data-overview-search-results]")!;
  const status = document.querySelector("[data-overview-search-status]")!;
  const settled = status.textContent;
  const input = document.querySelector<HTMLInputElement>("#search-input")!;
  input.value = "new";
  input.dispatchEvent(new Event("input"));
  await vi.advanceTimersByTimeAsync(500);
  expect(root.getAttribute("aria-busy")).toBe("true");
  expect(status.classList.contains("is-loading")).toBe(true);

  input.dispatchEvent(new CompositionEvent("compositionstart"));
  expect(root.getAttribute("aria-busy")).toBe("false");
  expect(status.classList.contains("is-loading")).toBe(false);
  expect(status.textContent).toBe(settled);
  expect(document.querySelector("[data-overview-search-list]")?.hasAttribute("data-updating")).toBe(false);
});

it("restores the settled graph status when a load is canceled", () => {
  vi.useFakeTimers();
  vi.stubGlobal("fetch", vi.fn(() => new Promise(() => {})));
  vi.stubGlobal("cytoscape", () => ({}));
  const canvas = document.createElement("div");
  const status = document.createElement("p");
  const details = {
    detailsPanel: null, hoverTitle: null, hoverFolder: null,
    hoverDegree: null, hoverIssues: null, openNote: null,
  };
  const runtime = createGraphRuntime({
    ...details,
    canvas, status, filterInput: null, spreadInput: null, spreadValue: null,
    isolateButton: null, isolateClearButton: null, minDegreeInput: null,
    minDegreeValue: null, collapseInput: null, collapseValue: null, fitButton: null,
    zoomMatchesButton: null, resetLayoutButton: null,
    labelModeInputs: [], searchModeInputs: [],
  }, canvas, details);
  setStatus(runtime, "Loaded 3 nodes and 2 edges.");
  scheduleLoad(runtime, "new", 0);
  expect(status.textContent).toBe("Loading graph…");
  expect(status.classList.contains("is-loading")).toBe(true);

  cancelGraphLoad(runtime);
  expect(status.textContent).toBe("Loaded 3 nodes and 2 edges.");
  expect(status.classList.contains("is-loading")).toBe(false);
});
