// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { initOverviewSearchResults } from "./overview-search";
import { renderOverviewBulkFixture, selectOverviewResult } from "./search-rendering.test-helpers";
import { searchResult, setURL } from "./search-test-helpers";

const button = (name: string): HTMLButtonElement => document.querySelector(`[data-library-${name}]`)!;
afterEach(() => { vi.useRealTimers(); document.body.innerHTML = ""; setURL("/"); });

it("keeps selections across pages, carries return position, and resets on a new search", async () => {
  vi.useFakeTimers();
  setURL("/library?q=notes&sort=path&limit=75");
  renderOverviewBulkFixture();
  document.querySelector("section")!.insertAdjacentHTML("beforeend", `<nav data-library-pagination><button data-library-previous></button><span data-library-page></span><button data-library-next></button></nav>`);
  const fetcher = vi.fn(async (url: RequestInfo | URL) => {
    const offset = new URL(String(url), location.origin).searchParams.get("offset");
    const rows = Array.from({ length: offset ? 25 : 50 }, (_, i) => {
      const key = `Note${Number(offset || 0) + i}`;
      return searchResult({ key, path: `/n/${key}`, title: key, wikilink: key });
    });
    return new Response(JSON.stringify(rows), { headers: { "X-BN-Search-Total": "100", "X-BN-Search-Limit": "75", "X-BN-Search-Cap": "50" } });
  });
  initOverviewSearchResults({ fetch: fetcher });
  await vi.runAllTimersAsync();
  selectOverviewResult();
  button("next").click();
  await vi.runAllTimersAsync();
  expect(document.querySelectorAll(".overview-result-link")).toHaveLength(25);
  expect(document.querySelector("[data-overview-selected-count]")!.textContent).toBe("1 selected (1 on other pages)");
  expect(button("next").disabled).toBe(true);
  const link = document.querySelector<HTMLAnchorElement>(".overview-result-link")!;
  expect(new URL(link.href).searchParams.get("return_to")).toContain("offset=50");
  selectOverviewResult();
  const copy = document.querySelector<HTMLButtonElement>("[data-overview-copy-wikilinks]")!;
  expect(copy.dataset.copyText).toBe("[[Note0]]\n[[Note50]]");
  button("previous").click();
  await vi.runAllTimersAsync();
  expect(document.querySelector<HTMLInputElement>(".overview-result-select input")!.checked).toBe(true);
  const input = document.querySelector<HTMLInputElement>("#search-input")!;
  input.value = "different";
  input.dispatchEvent(new Event("input"));
  await vi.runAllTimersAsync();
  expect(location.search).not.toContain("offset");
  expect(document.querySelector("[data-overview-selected-count]")!.textContent).toBe("0 selected");
});

it("keeps selection and retries the same page after an unanswered page read", async () => {
  vi.useFakeTimers();
  setURL("/library?q=notes");
  renderOverviewBulkFixture();
  document.querySelector("section")!.insertAdjacentHTML("beforeend", `<button data-overview-search-retry hidden></button><nav data-library-pagination><button data-library-previous></button><span data-library-page></span><button data-library-next></button></nav>`);
  let fail = true;
  const fetcher = vi.fn(async (url: RequestInfo | URL) => {
    if (String(url).includes("offset=50") && fail) { fail = false; throw new Error("offline"); }
    return new Response(JSON.stringify([searchResult()]), { headers: { "X-BN-Search-Total": "60", "X-BN-Search-Limit": "0", "X-BN-Search-Cap": "50" } });
  });
  initOverviewSearchResults({ fetch: fetcher });
  await vi.runAllTimersAsync();
  selectOverviewResult();
  button("next").click();
  await vi.runAllTimersAsync();
  expect(document.querySelector("[data-library-page]")!.textContent).toBe("Page unavailable");
  expect(button("next").disabled).toBe(true);
  expect(button("previous").disabled).toBe(false);
  document.querySelector<HTMLButtonElement>("[data-overview-search-retry]")!.click();
  await vi.runAllTimersAsync();
  expect(String(fetcher.mock.calls.at(-1)![0])).toContain("offset=50");
  expect(document.querySelector<HTMLInputElement>(".overview-result-select input")!.checked).toBe(true);
});
