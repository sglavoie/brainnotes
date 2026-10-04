// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { initOverviewSearchResults } from "./overview-search";
import { bindLibraryPosition, libraryResultID } from "./library-position";
import { renderOverviewBulkFixture } from "./search-rendering.test-helpers";
import { searchResult, setURL } from "./search-test-helpers";

const originalScroll = Object.getOwnPropertyDescriptor(HTMLElement.prototype, "scrollIntoView");

afterEach(() => {
  document.body.innerHTML = "";
  setURL("/");
  vi.restoreAllMocks();
  vi.useRealTimers();
  if (originalScroll) Object.defineProperty(HTMLElement.prototype, "scrollIntoView", originalScroll);
  else Reflect.deleteProperty(HTMLElement.prototype, "scrollIntoView");
});

it("restores a Unicode result after async rendering and carries its anchor in the note link", async () => {
  vi.useFakeTimers();
  const key = 'Books/Café [draft] #1';
  const library = `/library?q=notes&tag=work&sort=title#${libraryResultID(key)}`;
  setURL(library);
  renderOverviewBulkFixture();
  document.querySelector<HTMLInputElement>("#search-input")!.value = "notes";
  const scroll = vi.fn();
  // jsdom has no layout implementation.
  Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { value: scroll, configurable: true });
  initOverviewSearchResults({ fetch: vi.fn(async () => new Response(JSON.stringify([searchResult({ key })]))) });
  expect(scroll).not.toHaveBeenCalled();
  await vi.runAllTimersAsync();
  const link = document.querySelector<HTMLAnchorElement>(".overview-result-link")!;
  expect(document.activeElement).toBe(link);
  expect(scroll).toHaveBeenCalledOnce();
  expect(new URL(link.href).searchParams.get("return_to")).toBe(library);
});

const positionFixture = (): { list: HTMLElement; restore: () => void; scroll: ReturnType<typeof vi.fn> } => {
  setURL("/library?q=notes#library-result-Alpha");
  document.body.innerHTML = '<input><ul><li id="library-result-Alpha" data-overview-result-key="Alpha"><a class="overview-result-link" href="#">Alpha</a></li></ul>';
  const list = document.querySelector("ul")!;
  const scroll = vi.fn();
  list.querySelector("a")!.scrollIntoView = scroll;
  return { list, restore: bindLibraryPosition(list), scroll };
};

it.each(["pointerdown", "keydown", "wheel", "touchstart"])("does not interrupt %s while results load", (event) => {
  const { restore, scroll } = positionFixture();
  document.querySelector("input")!.focus();
  document.dispatchEvent(new Event(event));
  restore();
  expect(document.activeElement).toBe(document.querySelector("input"));
  expect(scroll).not.toHaveBeenCalled();
});

it("does not restore after a changed search or when the result has disappeared", () => {
  const changed = positionFixture();
  setURL("/library?q=other#library-result-Alpha");
  changed.restore();
  expect(changed.scroll).not.toHaveBeenCalled();
  const missing = positionFixture();
  missing.list.replaceChildren();
  missing.restore();
  expect(missing.scroll).not.toHaveBeenCalled();
});

it("restores only once rather than jumping again when results refresh", () => {
  const { restore, scroll } = positionFixture();
  restore();
  restore();
  expect(scroll).toHaveBeenCalledOnce();
});

it("records the clicked result for browser Back without adding a history entry", () => {
  const { list, restore } = positionFixture();
  restore();
  setURL("/library?q=notes");
  const replace = vi.spyOn(history, "replaceState");
  const link = list.querySelector("a")!;
  link.addEventListener("click", (event) => event.preventDefault());
  link.click();
  expect(location.hash).toBe("#library-result-Alpha");
  expect(replace).toHaveBeenCalledOnce();
});
