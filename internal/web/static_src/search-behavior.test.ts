// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import { dispatchFiltersChange } from "./filters";
import { initSearch, nextSearchActiveIndex } from "./search";
import { searchResult, setURL } from "./search-test-helpers";

const renderSearch = (): { input: HTMLInputElement; results: HTMLElement; feedback: HTMLElement } => {
  document.body.innerHTML = `
    <section data-search-box>
      <input id="search-input" aria-expanded="false" />
      <p data-search-keyboard-hint hidden>Keyboard help</p>
      <ul id="search-results" hidden></ul>
      <p data-search-feedback hidden></p>
    </section>
  `;
  return {
    input: document.getElementById("search-input") as HTMLInputElement,
    results: document.getElementById("search-results") as HTMLElement,
    feedback: document.querySelector("[data-search-feedback]") as HTMLElement,
  };
};

const typeQuery = (input: HTMLInputElement, query: string): void => {
  input.value = query;
  input.dispatchEvent(new Event("input", { bubbles: true }));
};

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  document.body.innerHTML = "";
  setURL("/");
});

it("shows keyboard help with results and hides it on dismissal", async () => {
  vi.useFakeTimers();
  const { input } = renderSearch();
  vi.stubGlobal("fetch", vi.fn(async () => ({
    ok: true, json: async () => [searchResult()],
  })));
  initSearch();
  input.focus();
  typeQuery(input, "note");
  await vi.runAllTimersAsync();
  const hint = document.querySelector<HTMLElement>("[data-search-keyboard-hint]")!;
  expect(hint.hidden).toBe(false);
  input.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
  expect(hint.hidden).toBe(true);
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
    document.getElementById("search-input")?.focus();
    setURL("/?tag=work");
    dispatchFiltersChange(window, {
      mode: "search",
      params: new URLSearchParams("tag=work"),
    });

    await vi.runAllTimersAsync();

    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock).toHaveBeenCalledWith("/api/search?tag=work", expect.objectContaining({
      headers: { Accept: "application/json" },
    }));
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

describe("search feedback", () => {
  it("explains empty results and failed requests without opening an empty listbox", async () => {
    vi.useFakeTimers();
    const { input, results, feedback } = renderSearch();
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({ ok: true, json: async () => [] })
      .mockResolvedValueOnce({ ok: false });
    vi.stubGlobal("fetch", fetchMock);
    initSearch();

    typeQuery(input, "missing");
    expect(feedback.hidden).toBe(true);
    await vi.runAllTimersAsync();
    expect(feedback.textContent).toBe("No matching notes.");
    expect(feedback.hidden).toBe(false);
    expect(results.hidden).toBe(true);
    expect(input.getAttribute("aria-expanded")).toBe("false");

    typeQuery(input, "broken");
    await vi.runAllTimersAsync();
    expect(feedback.textContent).toBe("Couldn't load matching notes. Check your connection, then select Retry search.");
    expect(results.hidden).toBe(true);

    typeQuery(input, "");
    expect(feedback.hidden).toBe(true);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("keeps current results and the highlighted option while the next search is pending", async () => {
    vi.useFakeTimers();
    const { input, results, feedback } = renderSearch();
    let resolveSecond: (value: unknown) => void = () => {};
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({ ok: true, json: async () => [searchResult()] })
      .mockReturnValueOnce(new Promise((resolve) => { resolveSecond = resolve; }));
    vi.stubGlobal("fetch", fetchMock);
    initSearch();

    typeQuery(input, "note");
    await vi.runAllTimersAsync();
    input.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true }));
    expect(input.getAttribute("aria-activedescendant")).toBe("search-option-0");

    typeQuery(input, "other");
    await vi.advanceTimersByTimeAsync(1000);
    expect(results.hidden).toBe(false);
    expect(results.childElementCount).toBe(1);
    expect(results.getAttribute("aria-busy")).toBe("true");
    expect(input.getAttribute("aria-activedescendant")).toBe("search-option-0");
    expect(feedback.hidden).toBe(true);
    expect(feedback.textContent).toBe("");

    resolveSecond({ ok: true, json: async () => [] });
    await vi.runAllTimersAsync();
    expect(results.hasAttribute("aria-busy")).toBe(false);
    expect(feedback.textContent).toBe("No matching notes.");
  });

  it("shows Searching… only after a delay when nothing is displayed", async () => {
    vi.useFakeTimers();
    const { input, feedback } = renderSearch();
    let resolveResponse: (value: unknown) => void = () => {};
    vi.stubGlobal("fetch", vi.fn(() => new Promise((resolve) => { resolveResponse = resolve; })));
    initSearch();

    typeQuery(input, "note");
    await vi.advanceTimersByTimeAsync(349);
    expect(feedback.hidden).toBe(true);
    await vi.advanceTimersByTimeAsync(1);
    expect(feedback.hidden).toBe(false);
    expect(feedback.textContent).toBe("Searching…");
    resolveResponse({ ok: true, json: async () => [searchResult()] });
    await vi.runAllTimersAsync();
    expect(feedback.hidden).toBe(true);
  });

  it("aborts a superseded in-flight request", async () => {
    vi.useFakeTimers();
    const { input } = renderSearch();
    const signals: AbortSignal[] = [];
    vi.stubGlobal("fetch", vi.fn((_url: string, init: RequestInit) => {
      signals.push(init.signal!);
      return new Promise(() => {});
    }));
    initSearch();

    typeQuery(input, "one");
    await vi.advanceTimersByTimeAsync(150);
    typeQuery(input, "two");
    expect(signals[0].aborted).toBe(true);
    await vi.advanceTimersByTimeAsync(150);
    expect(signals[1].aborted).toBe(false);
    input.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    expect(signals[1].aborted).toBe(true);
  });
});

describe("search dismissal", () => {
  it("cancels a search dismissed during the debounce", async () => {
    vi.useFakeTimers();
    const { input, feedback } = renderSearch();
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    initSearch();

    typeQuery(input, "note");
    input.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await vi.runAllTimersAsync();
    expect(fetchMock).not.toHaveBeenCalled();
    expect(feedback.hidden).toBe(true);
  });

  it.each(["Escape", "outside click", "focus leaving"])(
    "keeps a pending search dismissed after %s",
    async (dismissal) => {
      vi.useFakeTimers();
      const { input, results, feedback } = renderSearch();
      let resolveResponse: ((response: unknown) => void) | undefined;
      vi.stubGlobal("fetch", vi.fn(() => new Promise((resolve) => {
        resolveResponse = resolve;
      })));
      initSearch();

      typeQuery(input, "note");
      await vi.runAllTimersAsync();
      if (dismissal === "Escape") {
        input.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
      } else if (dismissal === "outside click") {
        document.body.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      } else {
        input.dispatchEvent(new FocusEvent("focusout", {
          bubbles: true,
          relatedTarget: document.body,
        }));
      }
      expect(feedback.hidden).toBe(true);
      resolveResponse?.({ ok: true, json: async () => [searchResult()] });
      await Promise.resolve();
      await Promise.resolve();
      expect(results.hidden).toBe(true);
      expect(feedback.hidden).toBe(true);
    }
  );
});
