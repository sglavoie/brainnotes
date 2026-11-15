// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { openConnection } from "./connection-dialog";
import { installDialogPolyfill, jsonResponse } from "./shared.test-helpers";

beforeEach(() => {
  document.documentElement.dataset.readOnly = "true";
  document.body.innerHTML = '<button id="trigger">Explore connection</button><a id="next" href="/n/beta">Beta</a>';
  vi.stubGlobal("fetch", vi.fn(async () => jsonResponse({ title: "Beta", excerpt: "Beta body", path: "/n/beta" })));
  installDialogPolyfill();
  vi.spyOn(HTMLDialogElement.prototype, "showModal").mockImplementation(function (this: HTMLDialogElement) {
    this.open = true;
    this.querySelector("button")!.focus();
  });
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  delete document.documentElement.dataset.readOnly;
  document.body.replaceChildren();
});

const opened = () => {
  const trigger = document.querySelector<HTMLButtonElement>("#trigger")!;
  openConnection({ source: "alpha", candidate: "beta" }, trigger);
  return { trigger, dialog: document.querySelector("dialog")! };
};

it.each(["dialog", "body"])("returns focus to the trigger when closing leaves focus in %s", (target) => {
  const { trigger, dialog } = opened();
  dialog.open = false;
  if (target === "body") dialog.querySelector("button")!.blur();
  dialog.dispatchEvent(new Event("close"));
  expect(document.activeElement).toBe(trigger);
  expect(dialog.isConnected).toBe(false);
});

it("preserves a new focus target before the queued close event arrives", () => {
  const { dialog } = opened();
  // Native close removes modal state before dispatching its queued close event.
  dialog.open = false;
  const next = document.querySelector<HTMLAnchorElement>("#next")!;
  next.focus();
  dialog.dispatchEvent(new Event("close"));
  expect(document.activeElement).toBe(next);
  expect(dialog.isConnected).toBe(false);
});
