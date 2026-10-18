// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { initOverviewSearchResults } from "./overview-search";
import { renderOverviewBulkFixture } from "./search-rendering.test-helpers";
import { searchResult, setURL } from "./search-test-helpers";

afterEach(() => { vi.useRealTimers(); document.body.innerHTML = ""; setURL("/"); });

it("keeps previous results marked as updating, aborts superseded reads and waits for IME", async () => {
  vi.useFakeTimers();
  renderOverviewBulkFixture();
  setURL("/library?q=old");
  const calls: AbortSignal[] = [];
  let resolve: (response: Response) => void = () => {};
  const fetcher = vi.fn((_url: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    calls.push(init!.signal!);
    return calls.length === 1 ? Promise.resolve(new Response(JSON.stringify([searchResult({ title: "Old" })])))
      : new Promise((done) => { resolve = done; });
  });
  initOverviewSearchResults({ fetch: fetcher });
  await vi.advanceTimersByTimeAsync(1);
  const input = document.querySelector<HTMLInputElement>("#search-input")!;
  const status = document.querySelector("[data-overview-search-status]")!;
  input.value = "new";
  input.dispatchEvent(new Event("input"));
  expect(status.textContent).not.toContain("Loading");
  expect(document.querySelector("[data-overview-search-list]")?.getAttribute("data-updating")).toBe("true");
  await vi.advanceTimersByTimeAsync(350);
  expect(status.textContent).toContain("Previous matches");
  input.dispatchEvent(new CompositionEvent("compositionstart"));
  expect(calls[1].aborted).toBe(true);
  input.value = "日本";
  input.dispatchEvent(new InputEvent("input", { isComposing: true }));
  await vi.advanceTimersByTimeAsync(500);
  expect(fetcher).toHaveBeenCalledTimes(2);
  resolve(new Response(JSON.stringify([searchResult({ title: "Stale" })])));
  await vi.advanceTimersByTimeAsync(1);
  expect(document.querySelector(".overview-result-title")?.textContent).toBe("Old");
  input.dispatchEvent(new CompositionEvent("compositionend"));
  await vi.advanceTimersByTimeAsync(150);
  expect(fetcher).toHaveBeenCalledTimes(3);
  resolve(new Response(JSON.stringify([searchResult({ title: "Current" })])));
  await vi.advanceTimersByTimeAsync(400);
  expect(document.querySelector(".overview-result-title")?.textContent).toBe("Current");
  expect(status.textContent).not.toContain("Updating");
});
