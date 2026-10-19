// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { initFacets } from "./facets";

afterEach(() => {
  document.body.innerHTML = "";
  history.replaceState(null, "", "/");
  vi.unstubAllGlobals();
});

const mount = (): { input: HTMLInputElement; retry: HTMLButtonElement } => {
  history.replaceState(null, "", "/library?q=body&tag=existing&sort=-modified&limit=2#results");
  document.body.innerHTML = `<section data-facet-controls>
    <input class="facet-input" /><ul class="facet-options" hidden></ul>
    <div data-facet-chips></div><p data-facet-status role="status"></p>
    <button data-facet-retry hidden>Retry filters</button>
  </section>`;
  initFacets();
  return { input: document.querySelector("input")!, retry: document.querySelector("[data-facet-retry]")! };
};

const payload = { tags: [{ value: "existing", count: 4 }, { value: "other", count: 2 }] };

it("keeps failures visible while typing, retries once, and preserves the search and selected chips", async () => {
  let resolveRetry!: (response: unknown) => void;
  const fetcher = vi.fn().mockRejectedValueOnce(new Error("offline"))
    .mockImplementationOnce(() => new Promise((resolve) => { resolveRetry = resolve; }));
  vi.stubGlobal("fetch", fetcher);
  const { input, retry } = mount();
  const originalURL = location.href;
  await vi.waitFor(() => expect(retry.hidden).toBe(false));
  input.focus();
  input.value = "existing";
  input.dispatchEvent(new Event("input"));
  expect(document.querySelector(".facet-options")!.textContent).toBe("Couldn't load filters.");
  retry.focus();
  retry.click();
  retry.click();
  expect(retry.getAttribute("aria-disabled")).toBe("true");
  expect(fetcher).toHaveBeenCalledTimes(2);
  resolveRetry({ ok: true, json: async () => payload });
  await vi.waitFor(() => expect(retry.hidden).toBe(true));
  expect(input.value).toBe("existing");
  expect(input).toBe(document.activeElement);
  expect(document.querySelectorAll(".facet-option")).toHaveLength(1);
  expect(document.querySelector(".facet-option")!.getAttribute("aria-selected")).toBe("true");
  expect(document.querySelector("[data-facet-chips]")!.textContent).toContain("(4)");
  expect(location.href).toBe(originalURL);
});

it("can retry repeated HTTP and JSON failures and keeps removals made during loading", async () => {
  let resolveRetry!: (response: unknown) => void;
  const fetcher = vi.fn().mockResolvedValueOnce({ ok: false })
    .mockResolvedValueOnce({ ok: true, json: async () => { throw new Error("bad JSON"); } })
    .mockImplementationOnce(() => new Promise((resolve) => { resolveRetry = resolve; }));
  vi.stubGlobal("fetch", fetcher);
  const { retry } = mount();
  await vi.waitFor(() => expect(retry.hidden).toBe(false));
  retry.click();
  await vi.waitFor(() => expect(retry.getAttribute("aria-disabled")).toBe("false"));
  expect(retry.hidden).toBe(false);
  retry.click();
  document.querySelector<HTMLButtonElement>(".facet-chip-remove")!.click();
  resolveRetry({ ok: true, json: async () => payload });
  await vi.waitFor(() => expect(retry.hidden).toBe(true));
  expect(new URL(location.href).searchParams.has("tag")).toBe(false);
  expect(document.querySelector<HTMLElement>("[data-facet-chips]")!.hidden).toBe(true);
  expect(document.querySelectorAll('[aria-selected="true"]')).toHaveLength(0);
});

it("treats a successful empty catalog as empty, not as a retryable failure", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: true, json: async () => ({}) }));
  const { input, retry } = mount();
  input.focus();
  await vi.waitFor(() => expect(document.querySelector(".facet-options")!.textContent).toBe("No matching facets."));
  expect(retry.hidden).toBe(true);
});

it("supports keyboard selection, wraparound, and Escape after retry", async () => {
  vi.stubGlobal("fetch", vi.fn().mockRejectedValueOnce(new Error("offline"))
    .mockResolvedValueOnce({ ok: true, json: async () => payload }));
  const { input, retry } = mount();
  await vi.waitFor(() => expect(retry.hidden).toBe(false));
  retry.click();
  await vi.waitFor(() => expect(retry.hidden).toBe(true));
  input.focus();
  const key = (value: string): void => { input.dispatchEvent(new KeyboardEvent("keydown", { key: value, cancelable: true })); };
  key("ArrowUp");
  expect(document.querySelector(".facet-option.is-active")!.textContent).toContain("other");
  key("ArrowDown");
  expect(document.querySelector(".facet-option.is-active")!.textContent).toContain("existing");
  key("ArrowUp");
  key("Enter");
  expect(new URL(location.href).searchParams.getAll("tag")).toEqual(["existing", "other"]);
  key("Escape");
  expect(input.getAttribute("aria-expanded")).toBe("false");
  expect(input.hasAttribute("aria-activedescendant")).toBe(false);
});
