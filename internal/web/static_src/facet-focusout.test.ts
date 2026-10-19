// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { initFacets } from "./facets";

afterEach(() => {
  document.body.innerHTML = "";
  history.replaceState(null, "", "/");
  vi.unstubAllGlobals();
});

it("collapses the filter combobox when Tab moves focus outside it", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => ({
    ok: true, json: async () => ({ tags: [{ value: "alpha", count: 2 }, { value: "beta", count: 1 }] }),
  })));
  document.body.innerHTML = `<section data-facet-controls>
    <input id="f" class="facet-input" aria-expanded="false" /><ul class="facet-options" hidden></ul>
    <button data-facet-clear-all type="button">Clear filters</button>
    <div data-facet-chips></div><p data-facet-status role="status"></p>
  </section><button id="outside" type="button">Next</button>`;
  initFacets();
  const input = document.querySelector<HTMLInputElement>(".facet-input")!;
  const options = document.querySelector<HTMLElement>(".facet-options")!;
  input.focus();
  await vi.waitFor(() => expect(options.querySelectorAll(".facet-option")).toHaveLength(2));
  input.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown" }));
  expect(input.getAttribute("aria-activedescendant")).toBe("f-option-0");

  const inside = document.querySelector<HTMLElement>("[data-facet-clear-all]")!;
  input.dispatchEvent(new FocusEvent("focusout", { bubbles: true, relatedTarget: inside }));
  expect(input.getAttribute("aria-expanded")).toBe("true");

  const outside = document.getElementById("outside")!;
  input.dispatchEvent(new FocusEvent("focusout", { bubbles: true, relatedTarget: outside }));
  expect(input.getAttribute("aria-expanded")).toBe("false");
  expect(options.hidden).toBe(true);
  expect(input.hasAttribute("aria-activedescendant")).toBe(false);
});
