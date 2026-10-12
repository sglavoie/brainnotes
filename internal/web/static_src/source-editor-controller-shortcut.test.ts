// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import { cleanup, LOADED, mount } from "./source-editor-controller.test-helpers";

afterEach(cleanup);

// The save shortcut. It is bound on the document rather than only in the
// editor's keymap, so it reaches Save from the toolbar and the conflict
// buttons too — and is therefore the one path into a save that the Save
// button's disabled state cannot speak for.
describe("the save shortcut", () => {
  it("saves on Ctrl/⌘+S from anywhere on the page and stops the browser's own handler", async () => {
    const harness = mount();
    await harness.controller.load();
    harness.surface().type("edited\n");

    const event = new KeyboardEvent("keydown", { key: "s", ctrlKey: true, cancelable: true, bubbles: true });
    document.body.dispatchEvent(event);
    expect(event.defaultPrevented).toBe(true);
    await vi.waitFor(() => expect(harness.client.save).toHaveBeenCalledTimes(1));
  });

  it("leaves an unmodified S alone", async () => {
    const harness = mount();
    await harness.controller.load();
    harness.surface().type("edited\n");

    document.body.dispatchEvent(new KeyboardEvent("keydown", { key: "s", cancelable: true, bubbles: true }));
    expect(harness.client.save).not.toHaveBeenCalled();
  });

  // The shortcut does not go through the Save button, so the button being
  // disabled refuses nothing. Without the same test in the save path, holding
  // Ctrl/⌘+S on an untouched note writes one save_id into the server's
  // idempotency ledger per keypress and reports each one as a success.
  it("saves nothing when the note is untouched", async () => {
    const harness = mount();
    await harness.controller.load();
    expect(harness.saveButton().disabled).toBe(true);

    const event = new KeyboardEvent("keydown", { key: "s", ctrlKey: true, cancelable: true, bubbles: true });
    document.body.dispatchEvent(event);
    await Promise.resolve();

    expect(harness.client.save).not.toHaveBeenCalled();
    expect(harness.status()).not.toContain("Saving");
  });

  // Ctrl/⌘+Shift+S is somebody else's chord — the browser's, an extension's —
  // and this listener is in the capture phase, so claiming it would swallow it
  // and do something the person did not ask for.
  it("leaves Ctrl/⌘+Shift+S to whatever else claimed it", async () => {
    const harness = mount();
    await harness.controller.load();
    harness.surface().type("edited\n");

    const event = new KeyboardEvent("keydown", {
      key: "s",
      ctrlKey: true,
      shiftKey: true,
      cancelable: true,
      bubbles: true,
    });
    document.body.dispatchEvent(event);

    expect(event.defaultPrevented).toBe(false);
    expect(harness.client.save).not.toHaveBeenCalled();
  });

  it("stops listening once destroyed", async () => {
    const harness = mount();
    await harness.controller.load();
    harness.surface().type("edited\n");
    harness.controller.destroy();

    document.body.dispatchEvent(new KeyboardEvent("keydown", { key: "s", ctrlKey: true, bubbles: true }));
    expect(harness.client.save).not.toHaveBeenCalled();
  });
});

it("retries a failed preview with the current draft without saving or editing it", async () => {
  const harness = mount({}, 10000);
  await harness.controller.load();
  await vi.waitFor(() => expect(harness.elements.preview?.innerHTML).toContain("Alpha body"));
  harness.client.preview.mockRejectedValueOnce(new Error("Offline."));
  harness.surface().type("draft one");
  harness.controller.panes.select("preview");
  const retry = document.querySelector<HTMLButtonElement>("[data-source-preview-retry]")!;
  await vi.waitFor(() => expect(retry.hidden).toBe(false));
  expect(harness.elements.previewStatus?.textContent).toContain("previous preview is outdated");
  expect(harness.elements.preview?.innerHTML).toContain("Alpha body");
  harness.surface().type("draft two");
  let resolve!: (html: string) => void;
  harness.client.preview.mockImplementationOnce(() => new Promise<string>(done => { resolve = done; }));
  retry.focus();
  retry.click();
  retry.click();
  expect(harness.client.preview).toHaveBeenCalledTimes(3);
  expect(harness.client.preview).toHaveBeenLastCalledWith("draft two");
  expect(retry.getAttribute("aria-disabled")).toBe("true");
  resolve("<p>draft two</p>");
  await vi.waitFor(() => expect(retry.hidden).toBe(true));
  expect(harness.elements.preview?.innerHTML).toBe("<p>draft two</p>");
  expect(harness.surface().getValue()).toBe("draft two");
  expect(harness.controller.dirty()).toBe(true);
  expect(harness.client.save).not.toHaveBeenCalled();
  expect(document.activeElement).toBe(document.querySelector('[data-source-pane="preview"]'));
});

it("offers retry after an initial preview failure and ignores replies after teardown", async () => {
  const harness = mount({ preview: vi.fn().mockRejectedValue(new Error("Offline.")) });
  await harness.controller.load();
  const retry = document.querySelector<HTMLButtonElement>("[data-source-preview-retry]")!;
  await vi.waitFor(() => expect(retry.hidden).toBe(false));
  expect(harness.elements.previewStatus?.textContent).toContain("No preview is available");
  let resolve!: (html: string) => void;
  harness.client.preview.mockImplementationOnce(() => new Promise<string>(done => { resolve = done; }));
  retry.click();
  expect(harness.client.preview).toHaveBeenLastCalledWith(LOADED);
  harness.controller.destroy();
  resolve("<p>late</p>");
  await Promise.resolve();
  expect(harness.elements.preview?.innerHTML).toBe("");
});
