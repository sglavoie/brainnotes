// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { createUndoToast } from "./properties-toast";

afterEach(() => {
  document.body.innerHTML = "";
});

const mount = (onUndo: (journalID: string) => void = () => {}, statusTarget?: () => HTMLElement | null) => {
  document.body.innerHTML = '<button data-trigger>Apply</button><p data-status></p><div data-host></div>';
  const trigger = document.querySelector<HTMLButtonElement>("[data-trigger]")!;
  const host = document.querySelector<HTMLElement>("[data-host]")!;
  const toast = createUndoToast(document, host, { onUndo, statusTarget });
  return { trigger, toast };
};

const show = { message: "Set status on 2 notes.", journalID: "entry-1", historyHref: "/history" };
const button = (name: "undo" | "dismiss"): HTMLButtonElement =>
  document.querySelector<HTMLButtonElement>(`[data-property-toast-${name}]`)!;

// jsdom keeps focus on a button that becomes hidden; a browser drops it to
// <body>. Blurring first reproduces what the browser does.
const pressAndLoseFocus = (target: HTMLButtonElement): void => {
  target.focus();
  target.blur();
  target.click();
};

it("announces through a status region that is rendered before the toast appears", () => {
  const { toast } = mount();
  const live = document.querySelector<HTMLElement>('[role="status"]')!;
  expect(live.isConnected).toBe(true);
  expect(live.hidden).toBe(false);
  expect(live.contains(toast.element)).toBe(true);

  toast.show(show);
  expect(live.textContent).toContain("Set status on 2 notes.");
  expect(toast.element.hidden).toBe(false);
});

it("returns focus to the trigger on Dismiss", () => {
  const { trigger, toast } = mount();
  trigger.focus();
  toast.show(show);
  pressAndLoseFocus(button("dismiss"));
  expect(toast.element.hidden).toBe(true);
  expect(document.activeElement).toBe(trigger);
});

it("moves focus to the status line when Undo hides the toast", () => {
  const status = () => document.querySelector<HTMLElement>("[data-status]");
  const onUndo = vi.fn();
  const { trigger, toast } = mount(onUndo, status);
  trigger.focus();
  toast.show(show);
  pressAndLoseFocus(button("undo"));
  expect(onUndo).toHaveBeenCalledWith("entry-1");
  expect(document.activeElement).toBe(status());
  expect(status()!.tabIndex).toBe(-1);
});

it("falls back to the trigger when Undo has no status line to land on", () => {
  const { trigger, toast } = mount();
  trigger.focus();
  toast.show(show);
  pressAndLoseFocus(button("undo"));
  expect(document.activeElement).toBe(trigger);
});

it("keeps focus inside a toast that stays up to report the undo's progress", () => {
  let toast: ReturnType<typeof createUndoToast> | null = null;
  const mounted = mount(() => toast?.show({ ...show, message: "Restoring note…", journalID: "" }));
  toast = mounted.toast;
  toast.show(show);
  pressAndLoseFocus(button("undo"));
  expect(document.activeElement).toBe(document.querySelector("[data-property-toast-message]"));
});

it("leaves focus alone when it did not fall to the page body", () => {
  const { trigger, toast } = mount();
  trigger.focus();
  toast.show(show);
  const elsewhere = document.createElement("input");
  document.body.appendChild(elsewhere);
  elsewhere.focus();
  button("dismiss").click();
  expect(document.activeElement).toBe(elsewhere);
});
