// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { initSearch } from "./search";
import { searchResult } from "./search-test-helpers";

const key = (target: HTMLElement, value: string): void => {
  target.dispatchEvent(new KeyboardEvent("keydown", { key: value, bubbles: true, cancelable: true }));
};

const setup = () => {
  vi.useFakeTimers();
  document.body.innerHTML = `<button id="origin">Read note</button><button id="other">Other</button>
    <section data-search-box><input id="search-input"><ul id="search-results" hidden></ul>
      <div data-search-feedback hidden><span data-search-feedback-message></span>
        <button data-search-retry hidden>Retry search</button></div></section>`;
  const fetcher = vi.fn().mockResolvedValue({ ok: false });
  vi.stubGlobal("fetch", fetcher);
  initSearch();
  const origin = document.querySelector<HTMLButtonElement>("#origin")!;
  const other = document.querySelector<HTMLButtonElement>("#other")!;
  const input = document.querySelector("input")!;
  const retry = document.querySelector<HTMLButtonElement>("[data-search-retry]")!;
  return { origin, other, input, retry, fetcher };
};

afterEach(() => {
  document.body.innerHTML = "";
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

it("returns focus without scrolling and dismisses a late search result", async () => {
  const { origin, input, fetcher } = setup();
  origin.focus();
  key(origin, "/");
  let resolve!: (value: unknown) => void;
  fetcher.mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
  input.value = "body";
  input.dispatchEvent(new Event("input"));
  await vi.runAllTimersAsync();
  const focus = vi.spyOn(origin, "focus");
  key(input, "Escape");
  expect(document.activeElement).toBe(origin);
  expect(focus).toHaveBeenCalledWith({ preventScroll: true });
  resolve({ ok: true, json: async () => [searchResult()] });
  await vi.runAllTimersAsync();
  expect(document.querySelector("ul")!.hidden).toBe(true);
});

it("returns from the retry button without sending another request", async () => {
  const { origin, input, retry, fetcher } = setup();
  origin.focus();
  key(origin, "/");
  input.value = "body";
  input.dispatchEvent(new Event("input"));
  await vi.runAllTimersAsync();
  retry.focus();
  key(retry, "Escape");
  await vi.runAllTimersAsync();
  expect(document.activeElement).toBe(origin);
  expect(fetcher).toHaveBeenCalledTimes(1);
});

it.each(["removed", "disabled", "hidden"])("leaves focus in search when the origin is %s", (state) => {
  const { origin, input } = setup();
  origin.focus();
  key(origin, "/");
  if (state === "removed") origin.remove();
  if (state === "disabled") origin.disabled = true;
  if (state === "hidden") origin.hidden = true;
  key(input, "Escape");
  expect(document.activeElement).toBe(input);
});

it("forgets the return target after focus leaves and search is reopened normally", () => {
  const { origin, other, input } = setup();
  origin.focus();
  key(origin, "/");
  other.focus();
  input.focus();
  key(input, "Escape");
  expect(document.activeElement).toBe(input);
});

it("keeps focus in search when it was opened without the shortcut", () => {
  const { origin, input } = setup();
  origin.focus();
  input.focus();
  key(input, "Escape");
  expect(document.activeElement).toBe(input);
});
