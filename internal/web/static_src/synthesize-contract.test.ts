// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import { initSynthesize } from "./synthesize";
import {
  memoryStorage,
  query,
  routedFetch,
  start,
  workbench,
} from "./synthesize.test-helpers";

afterEach(() => {
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

describe("synthesis DOM contract", () => {
  it("exposes one column stack and stable hooks for every control", async () => {
    await start({ fetch: routedFetch(), storage: memoryStorage(), clipboard: null });
    for (const selector of [
      "[data-synthesize-source-list]",
      "[data-synthesize-selection-status]",
      "[data-synthesize-suggestions='balanced']",
      "[data-synthesize-suggestions='rediscover']",
      "[data-synthesize-suggestions='connect']",
      "[data-synthesize-suggestions-status]",
      "[data-synthesize-search-form]",
      "[data-synthesize-search-input]",
      "[data-synthesize-search-results]",
      "[data-synthesize-search-status]",
      "[data-synthesize-title]",
      "[data-synthesize-title-error]",
      "[data-synthesize-folder]",
      "[data-synthesize-folder-error]",
      "[data-synthesize-purpose]",
      "[data-synthesize-prompt]",
      "[data-synthesize-draft]",
      "[data-synthesize-draft-error]",
      "[data-synthesize-prepare]",
      "[data-synthesize-copy]",
      "[data-synthesize-discard]",
      "[data-synthesize-context-status]",
      "[data-synthesize-manifest]",
      "[data-synthesize-packet]",
      "[data-synthesize-generate]",
      "[data-synthesize-generate-status]",
      "[data-synthesize-candidate-panel]",
      "[data-synthesize-candidate-rendered]",
      "[data-synthesize-candidate-markdown]",
      "[data-synthesize-candidate-replace]",
      "[data-synthesize-candidate-append]",
      "[data-synthesize-candidate-copy]",
      "[data-synthesize-candidate-dismiss]",
      "[data-synthesize-destination]",
      "[data-synthesize-preview]",
      "[data-synthesize-apply]",
      "[data-synthesize-preview-status]",
      "[data-synthesize-preview-panel]",
      "[data-synthesize-preview-destination]",
      "[data-synthesize-preview-expiry]",
      "[data-synthesize-preview-rendered]",
      "[data-synthesize-preview-markdown]",
      "[data-synthesize-preview-sources]",
      "[data-synthesize-warning]",
    ]) {
      expect(document.querySelectorAll(selector)).toHaveLength(1);
    }
    for (const selector of [
      "[data-synthesize-title-error]",
      "[data-synthesize-folder-error]",
      "[data-synthesize-draft-error]",
      "[data-synthesize-candidate-panel]",
      "[data-synthesize-preview-panel]",
    ]) {
      expect(query<HTMLElement>(selector).hidden).toBe(true);
    }
    expect(query<HTMLInputElement>("[data-synthesize-title]").getAttribute("aria-describedby")).toBe(
      "synthesize-title-error"
    );
    expect(query<HTMLButtonElement>("[data-synthesize-generate]").disabled).toBe(true);
    expect(query<HTMLButtonElement>("[data-synthesize-apply]").disabled).toBe(true);
    for (const selector of [
      "[data-synthesize-generate-status]",
      "[data-synthesize-preview-status]",
    ]) {
      expect(query<HTMLElement>(selector).getAttribute("aria-live")).toBe("polite");
    }
    expect(workbench().querySelectorAll(".synthesize-column")).toHaveLength(2);
    expect(query<HTMLElement>("[data-synthesize-warning]").hidden).toBe(true);
    expect(
      query<HTMLElement>("[data-synthesize-panel='balanced']").getAttribute("role")
    ).toBe("tabpanel");
    expect(
      Array.from(document.querySelectorAll<HTMLSelectElement>("[data-synthesize-purpose] option")).map(
        (option) => option.value
      )
    ).toEqual(["synthesis", "contradictions", "questions"]);
  });

  it("does nothing on a page with no synthesis root", () => {
    document.body.innerHTML = "<main></main>";
    expect(initSynthesize({ fetch: routedFetch(), storage: null, clipboard: null })).toBeNull();
  });
});
