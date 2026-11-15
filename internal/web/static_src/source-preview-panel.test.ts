// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { createPreviewPanel } from "./source-preview-panel";
import { readSourceEditorElements } from "./source-editor-elements";
import { editorMarkup } from "./source-editor.test-helpers";

const setup = () => {
  vi.useFakeTimers();
  document.body.innerHTML = editorMarkup();
  const panel = document.querySelector<HTMLElement>("[data-source-editor]")!;
  const pane = panel.querySelector<HTMLElement>('[data-source-pane="preview"]')!;
  let visible = false;
  let resize = () => {};
  const disconnect = vi.fn();
  vi.stubGlobal("ResizeObserver", class {
    constructor(callback: () => void) { resize = callback; }
    observe = vi.fn();
    disconnect = disconnect;
  });
  vi.spyOn(pane, "getClientRects").mockImplementation(() => (visible ? [new DOMRect()] : []) as unknown as DOMRectList);
  let content = "loaded";
  const render = vi.fn(async (text: string) => `<p>${text}</p>`);
  const preview = createPreviewPanel(readSourceEditorElements(panel)!, render, () => content, 50);
  return { preview, render, disconnect, resize: () => resize(),
    show: () => { visible = true; resize(); }, hide: () => { visible = false; resize(); },
    edit: (text: string) => { content = text; preview.schedule(text); } };
};

afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.useRealTimers(); document.body.replaceChildren(); });

it("defers hidden renders and reveals the latest draft after layout changes", async () => {
  const ui = setup();
  ui.resize();
  expect(ui.render).not.toHaveBeenCalled();
  ui.preview.flush("loaded");
  ui.edit("first");
  ui.edit("latest");
  await vi.advanceTimersByTimeAsync(50);
  expect(ui.render).not.toHaveBeenCalled();
  ui.show();
  expect(ui.render).toHaveBeenCalledExactlyOnceWith("latest");
  await Promise.resolve();
  expect(document.querySelector("[data-source-preview]")!.textContent).toBe("latest");
  ui.resize();
  expect(ui.render).toHaveBeenCalledTimes(1);
  ui.hide();
  ui.edit("newer");
  await vi.advanceTimersByTimeAsync(50);
  expect(ui.render).toHaveBeenCalledTimes(1);
  ui.show();
  expect(ui.render).toHaveBeenLastCalledWith("newer");
  ui.preview.dispose();
  expect(ui.disconnect).toHaveBeenCalledOnce();
});

it("checks visibility when a queued render runs, and ignores reveal after disposal", async () => {
  const ui = setup();
  ui.show();
  ui.edit("queued");
  ui.hide();
  await vi.advanceTimersByTimeAsync(50);
  expect(ui.render).not.toHaveBeenCalled();
  ui.preview.dispose();
  ui.show();
  expect(ui.render).not.toHaveBeenCalled();
});

it("preserves typing debounce when an already visible pane resizes", async () => {
  const ui = setup();
  ui.show();
  ui.preview.flush("loaded");
  ui.edit("queued");
  ui.resize();
  expect(ui.render).toHaveBeenCalledExactlyOnceWith("loaded");
  await vi.advanceTimersByTimeAsync(50);
  expect(ui.render).toHaveBeenLastCalledWith("queued");
  ui.preview.dispose();
});
