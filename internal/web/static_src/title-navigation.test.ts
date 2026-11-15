// @vitest-environment jsdom
import { afterEach, beforeAll, expect, it, vi } from "vitest";
import { initTitleNavigation, prepareTitleNavigation } from "./title-navigation";

const motion = Object.assign(new EventTarget(), { matches: true });
beforeAll(() => {
  vi.stubGlobal("matchMedia", () => motion);
  vi.stubGlobal("CSS", { supports: () => true });
  initTitleNavigation();
});
afterEach(() => {
  window.dispatchEvent(new Event("pageshow"));
  document.body.replaceChildren();
  motion.matches = true;
});

it.each([
  ['<a class="overview-result-link" href="/n/Alpha"><span class="overview-result-title">Alpha</span><p>Excerpt</p></a>', '.overview-result-title'],
  ['<h2 class="brief-card-title"><a href="/n/Alpha">Alpha</a></h2>', 'h2'],
  ['<a class="pinned-note-title" href="/n/Alpha">Alpha</a>', 'a'],
  ['<div class="note-peek"><h2 id="note-peek-title">Alpha</h2><a href="/n/Alpha">Open note</a></div>', 'h2'],
  ['<div class="connection-saved"><h3>Alpha</h3><div class="connection-saved-actions"><a href="/n/Alpha">Open note</a></div></div>', 'h3'],
])("names only the activated title and restores cancelled navigation: %s", async (markup, selector) => {
  document.body.innerHTML = '<h1 class="reading-title">Source</h1>' + markup;
  const title = document.querySelector<HTMLElement>(selector)!;
  const source = document.querySelector<HTMLElement>('h1')!;
  const link = document.querySelector('a')!;
  link.addEventListener('click', (event) => {
    expect(title.style.viewTransitionName).toBe('thought-title');
    expect(source.style.viewTransitionName).toBe('none');
    event.preventDefault();
  }, { once: true });
  link.click();
  await Promise.resolve();
  expect(title.style.viewTransitionName).toBe('');
  expect(source.style.viewTransitionName).toBe('');
});

it("replaces an earlier source and resets on history return or motion change", () => {
  document.body.innerHTML = '<h1 class="reading-title">Source</h1><span>First</span><b>Second</b>';
  const first = document.querySelector('span')!;
  const second = document.querySelector('b')!;
  prepareTitleNavigation(first);
  prepareTitleNavigation(second);
  expect(first.style.viewTransitionName).toBe('');
  expect(second.style.viewTransitionName).toBe('thought-title');
  window.dispatchEvent(new Event('pageshow'));
  expect(second.style.viewTransitionName).toBe('');
  prepareTitleNavigation(first);
  motion.matches = false; motion.dispatchEvent(new Event('change'));
  expect(first.style.viewTransitionName).toBe('');
  prepareTitleNavigation(second);
  expect(second.style.viewTransitionName).toBe('');
});

it.each([{ ctrlKey: true }, { metaKey: true }, { shiftKey: true }, { button: 1 }])("does not name a title for modified activation %j", (options) => {
  document.body.innerHTML = '<a class="pinned-note-title" href="/n/Alpha">Alpha</a>';
  const link = document.querySelector('a')!;
  link.addEventListener('click', (event) => {
    expect(link.style.viewTransitionName || '').toBe(''); event.preventDefault();
  });
  link.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, ...options }));
});
