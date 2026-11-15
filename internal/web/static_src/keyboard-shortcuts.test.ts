// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { acceptsShortcutKey, allowsSingleKey } from "./keyboard-guard";
import { matchShortcut } from "./keyboard-shortcuts";
import { initShortcutPreference } from "./keyboard-preference";
import { isSearchShortcut } from "./search-shortcut";

afterEach(() => {
  document.body.innerHTML = "";
  delete document.documentElement.dataset.shortcuts;
  localStorage.clear();
  vi.restoreAllMocks();
});

it.each([["b", "/"], ["l", "/library"], ["t", "/threads"], ["p", "/pinned"], ["g", "/graph"]])(
  "matches g then %s only within one second", (key, path) => {
    const first = matchShortcut("g", 100, null, true);
    expect(first).toEqual({ action: "", prefix: 100 });
    expect(matchShortcut(key, 1099, first.prefix, true)).toEqual({ action: path, prefix: null });
    expect(matchShortcut(key, 1100, first.prefix, true).action).toBe("");
  }
);

it.each(["j", "k", "Enter", "o", "x", "e", "?"])("matches %s without a chord", (key) => {
  expect(matchShortcut(key, 100, null, true)).toEqual({ action: key, prefix: null });
});

it("cancels a chord on an unrelated or guarded key", () => {
  expect(matchShortcut("z", 120, 100, true)).toEqual({ action: "", prefix: null });
  expect(matchShortcut("l", 120, 100, false)).toEqual({ action: "", prefix: null });
});

it.each(["ctrlKey", "metaKey", "altKey", "shiftKey", "isComposing", "repeat"])("ignores %s", (flag) => {
  expect(acceptsShortcutKey(new KeyboardEvent("keydown", { key: "g", [flag]: true }))).toBe(false);
});

it("accepts Shift producing ? but ignores handled keys", () => {
  const event = new KeyboardEvent("keydown", { key: "?", shiftKey: true, cancelable: true });
  expect(acceptsShortcutKey(event)).toBe(true);
  event.preventDefault();
  expect(acceptsShortcutKey(event)).toBe(false);
});

it.each(['<input>', '<textarea></textarea>', '<select></select>', '<div contenteditable><span></span></div>',
  '<div role="textbox"><span></span></div>', '<div id="graph-canvas"><span></span></div>',
  '<section data-source-editor><button></button></section>'])("yields to %s including search", (markup) => {
  document.body.innerHTML = markup;
  const target = document.querySelector("span, button") || document.body.firstElementChild!;
  target.addEventListener("keydown", (event) => {
    expect(allowsSingleKey(event as KeyboardEvent)).toBe(false);
    expect(isSearchShortcut(event as KeyboardEvent)).toBe(false);
  });
  target.dispatchEvent(new KeyboardEvent("keydown", { key: "/" }));
});

it.each(['<dialog open></dialog>', '<div role="dialog"></div>', '<div role="alertdialog"></div>'])("yields to open %s", (markup) => {
  document.body.innerHTML = markup;
  expect(allowsSingleKey(new KeyboardEvent("keydown", { key: "g" }))).toBe(false);
});

it("ignores closed dialogs", () => {
  document.body.innerHTML = '<dialog></dialog><div role="dialog" hidden></div>';
  expect(allowsSingleKey(new KeyboardEvent("keydown", { key: "g" }))).toBe(true);
});

it("restores the off preference and gates the independent search consumer", () => {
  localStorage.setItem("bn.shortcuts", "off");
  document.body.innerHTML = '<input type="checkbox" data-shortcuts-toggle checked>';
  initShortcutPreference(document);
  const toggle = document.querySelector("input")!;
  expect(toggle.checked).toBe(false);
  expect(isSearchShortcut(new KeyboardEvent("keydown", { key: "/" }))).toBe(false);
  toggle.click();
  expect(localStorage.getItem("bn.shortcuts")).toBe("on");
  expect(isSearchShortcut(new KeyboardEvent("keydown", { key: "/" }))).toBe(true);
});

it("keeps the current-page preference when storage throws", () => {
  vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => { throw new Error("unavailable"); });
  vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("unavailable"); });
  document.body.innerHTML = '<input type="checkbox" data-shortcuts-toggle>';
  initShortcutPreference(document);
  const toggle = document.querySelector("input")!;
  expect(toggle.checked).toBe(true);
  toggle.click();
  expect(allowsSingleKey(new KeyboardEvent("keydown", { key: "?" }))).toBe(false);
});
