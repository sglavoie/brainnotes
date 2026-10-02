// @vitest-environment jsdom
import { afterEach, expect, it } from "vitest";
import { bindSearchShortcut } from "./search-shortcut";

let unbind = () => {};
const setup = (): HTMLInputElement => {
  document.body.innerHTML = '<input id="search"><div id="other" tabindex="0"></div>';
  const input = document.querySelector("input")!;
  unbind = bindSearchShortcut(input);
  return input;
};
const press = (target: Element, init: KeyboardEventInit = {}): KeyboardEvent => {
  const event = new KeyboardEvent("keydown", { key: "/", bubbles: true, cancelable: true, ...init });
  target.dispatchEvent(event);
  return event;
};
afterEach(() => { unbind(); document.body.innerHTML = ""; });

it("focuses search from a noneditable control without entering a slash", () => {
  const input = setup();
  expect(press(document.querySelector("div")!).defaultPrevented).toBe(true);
  expect(document.activeElement).toBe(input);
  expect(input.value).toBe("");
});

it.each(['<input>', '<textarea></textarea>', '<select></select>',
  '<div contenteditable="true"><span>typing</span></div>',
  '<div role="textbox"><span>typing</span></div>',
])("leaves typing alone in %s", (markup) => {
  const input = setup();
  const host = document.createElement("section");
  host.innerHTML = markup;
  document.body.append(host);
  expect(press(host.querySelector("span") || host.firstElementChild!).defaultPrevented).toBe(false);
  expect(document.activeElement).not.toBe(input);
});

it.each(['<dialog open></dialog>', '<div role="dialog"></div>', '<div role="alertdialog"></div>'])(
  "does not move focus out of %s", (markup) => {
    const input = setup();
    document.body.insertAdjacentHTML("beforeend", markup);
    expect(press(document.body).defaultPrevented).toBe(false);
    expect(document.activeElement).not.toBe(input);
  }
);

it.each([{ ctrlKey: true }, { metaKey: true }, { altKey: true }, { isComposing: true }, { repeat: true }])(
  "ignores modified or composing shortcuts: %j", (init) => {
    const input = setup();
    expect(press(document.body, init).defaultPrevented).toBe(false);
    expect(document.activeElement).not.toBe(input);
  }
);

it("allows search when a dialog is closed", () => {
  const input = setup();
  document.body.insertAdjacentHTML("beforeend", '<dialog></dialog><div role="dialog" hidden></div>');
  press(document.body);
  expect(document.activeElement).toBe(input);
});
