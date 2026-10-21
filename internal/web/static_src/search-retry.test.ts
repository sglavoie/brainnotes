// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { initSearch } from "./search";
import { searchResult } from "./search-test-helpers";

const setup = () => {
  vi.useFakeTimers();
  history.replaceState(null, "", "/library?q=body&tag=work&sort=title");
  document.body.innerHTML = `
    <section data-search-box>
      <input id="search-input"><ul id="search-results" hidden></ul>
      <div data-search-feedback hidden><span data-search-feedback-message></span>
        <button data-search-retry hidden>Retry search</button></div>
    </section><div data-facet-controls data-facet-mode="search"></div>`;
  const fetcher = vi.fn().mockResolvedValueOnce({ ok: false });
  vi.stubGlobal("fetch", fetcher);
  initSearch();
  const input = document.querySelector("input")!;
  input.focus();
  return { input, fetcher, retry: document.querySelector("button")!,
    feedback: document.querySelector<HTMLElement>("[data-search-feedback]")!,
    results: document.querySelector("ul")! };
};

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  document.body.innerHTML = "";
  history.replaceState(null, "", "/");
});

it("retries the current query and filters once, then hides retry and restores input focus", async () => {
  const { input, fetcher, retry, results } = setup();
  await vi.runAllTimersAsync();
  expect(retry.hidden).toBe(false);
  const initialURL = fetcher.mock.calls[0][0];
  expect(initialURL).toContain("tag=work");
  expect(initialURL).toContain("q=body");
  fetcher.mockResolvedValueOnce({ ok: true, json: async () => [searchResult()] });
  retry.focus();
  retry.click();
  expect(retry.hidden).toBe(true);
  expect(document.activeElement).toBe(input);
  await vi.runAllTimersAsync();
  expect(fetcher).toHaveBeenCalledTimes(2);
  expect(fetcher.mock.calls[1][0]).toBe(initialURL);
  expect(results.hidden).toBe(false);
  expect(retry.hidden).toBe(true);
});

it.each(["Escape", "outside", "blur"])("dismisses a pending retry after %s without showing its late failure", async (method) => {
  const { input, fetcher, retry, results, feedback } = setup();
  await vi.runAllTimersAsync();
  let rejectRequest: (reason: Error) => void = () => {};
  fetcher.mockImplementationOnce(() => new Promise((_resolve, reject) => { rejectRequest = reject; }));
  retry.click();
  await vi.runAllTimersAsync();
  if (method === "Escape") input.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
  else if (method === "outside") document.body.click();
  else input.blur();
  rejectRequest(new Error("offline"));
  await vi.runAllTimersAsync();
  expect(feedback.hidden).toBe(true);
  expect(results.hidden).toBe(true);
  expect(retry.hidden).toBe(true);
});

it("dismisses with Escape from the retry button without submitting a new request", async () => {
  const { input, fetcher, retry, feedback } = setup();
  await vi.runAllTimersAsync();
  retry.focus();
  retry.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
  await vi.runAllTimersAsync();
  expect(fetcher).toHaveBeenCalledTimes(1);
  expect(feedback.hidden).toBe(true);
  expect(document.activeElement).toBe(input);
});

it("hides Retry when the query is changed or cleared and offers it for network errors", async () => {
  const { input, fetcher, retry, feedback } = setup();
  await vi.runAllTimersAsync();
  fetcher.mockRejectedValueOnce(new Error("offline"));
  input.value = "new query";
  input.dispatchEvent(new Event("input"));
  expect(retry.hidden).toBe(true);
  await vi.runAllTimersAsync();
  expect(fetcher.mock.calls[1][0]).toContain("q=new+query");
  expect(retry.hidden).toBe(false);
  history.replaceState(null, "", "/library");
  input.value = "";
  input.dispatchEvent(new Event("input"));
  expect(retry.hidden).toBe(true);
  expect(feedback.hidden).toBe(true);
});

it("shows a rejected query's reason instead of offering Retry", async () => {
  const { fetcher, retry, feedback } = setup();
  await vi.runAllTimersAsync();
  fetcher.mockResolvedValueOnce(new Response(JSON.stringify({ error: { message: "unknown field: foo" } }), { status: 400 }));
  const input = document.querySelector("input")!;
  input.value = "foo:bar";
  input.dispatchEvent(new Event("input"));
  await vi.runAllTimersAsync();
  expect(feedback.hidden).toBe(false);
  expect(feedback.textContent).toContain("unknown field: foo. Edit the query to continue.");
  expect(retry.hidden).toBe(true);
});
