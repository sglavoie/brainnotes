// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { initSearch } from "./search";
import { dispatchFiltersChange } from "./filters";
import { searchResult, setURL } from "./search-test-helpers";

const setup = (filtered = false) => {
  vi.useFakeTimers();
  document.body.innerHTML = `
    <section data-search-box>
      <input id="search-input" />
      <a data-search-library href="/library" hidden>Open results in Library</a>
      <ul id="search-results" hidden></ul>
      <p data-search-feedback hidden></p>
    </section>
    ${filtered ? '<div data-facet-controls data-facet-mode="search"></div>' : ''}`;
  vi.stubGlobal("fetch", vi.fn(async () => ({ ok: true, json: async () => [searchResult()] })));
  initSearch();
  const input = document.querySelector("input")!;
  const link = document.querySelector("a")!;
  const click = vi.spyOn(link, "click").mockImplementation(() => {});
  const type = (value: string) => {
    input.value = value;
    input.dispatchEvent(new Event("input", { bubbles: true }));
  };
  const key = (value: string, isComposing = false) => {
    const event = new KeyboardEvent("keydown", { key: value, isComposing, bubbles: true, cancelable: true });
    input.dispatchEvent(event);
    return event;
  };
  return { input, link, click, type, key };
};

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  document.body.innerHTML = "";
  setURL("/");
});

it("opens the typed query with Enter even before suggestions arrive, without unrelated page filters", () => {
  setURL("/graph?tag=private&expr=tag:old&layout=grid");
  const { link, click, type, key } = setup();
  expect(link.hidden).toBe(true);
  type(" café & books ");
  expect(link.getAttribute("href")).toBe("/library?q=caf%C3%A9+%26+books");
  expect(link.hidden).toBe(false);
  expect(key("Enter", true).defaultPrevented).toBe(false);
  expect(click).not.toHaveBeenCalled();
  expect(key("Enter").defaultPrevented).toBe(true);
  expect(click).toHaveBeenCalledOnce();
  type("");
  expect(link.hidden).toBe(true);
  expect(key("Enter").defaultPrevented).toBe(false);
});

it("keeps the Library's full filter shape, including repeated facets, but drops unrelated parameters", () => {
  setURL("/library?tag=work&tag=books&expr=status:wip&sort=-modified&limit=12&relative_to=Hub&junk=x");
  const { link, type } = setup(true);
  type("notes");
  const params = new URL(link.href).searchParams;
  expect(params.get("q")).toBe("notes");
  expect(params.getAll("tag")).toEqual(["work", "books"]);
  expect(params.get("expr")).toBe("status:wip");
  expect(params.get("sort")).toBe("-modified");
  expect(params.get("limit")).toBe("12");
  expect(params.get("relative_to")).toBe("Hub");
  expect(params.has("junk")).toBe(false);
  type("");
  expect(link.hidden).toBe(false);
  setURL("/library");
  dispatchFiltersChange(window, { mode: "search", params: new URLSearchParams() });
  expect(link.hidden).toBe(true);
});

it("keeps selected-note Enter behavior and allows tabbing to the Library link", async () => {
  const { input, link, click, type, key } = setup();
  type("note");
  await vi.runAllTimersAsync();
  input.dispatchEvent(new FocusEvent("focusout", { bubbles: true, relatedTarget: link }));
  expect(link.hidden).toBe(false);
  const note = document.querySelector<HTMLAnchorElement>(".search-result")!;
  const openNote = vi.spyOn(note, "click").mockImplementation(() => {});
  key("ArrowDown");
  key("Enter");
  expect(openNote).toHaveBeenCalledOnce();
  expect(click).not.toHaveBeenCalled();
  key("Escape");
  expect(link.hidden).toBe(true);
});

it.each([true, false])("keeps the Library handoff available after an empty or failed search (ok=%s)", async (ok) => {
  const { link, click, type, key } = setup();
  vi.stubGlobal("fetch", vi.fn(async () => ({ ok, json: async () => [] })));
  type("missing");
  await vi.runAllTimersAsync();
  expect(link.hidden).toBe(false);
  key("Enter");
  expect(click).toHaveBeenCalledOnce();
});
