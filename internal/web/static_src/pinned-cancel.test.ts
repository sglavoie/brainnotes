// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { initPinnedList } from "./pinned-list";

afterEach(() => { document.body.innerHTML = ""; });

const pointer = (type: string, y = 100): void => {
  window.dispatchEvent(new MouseEvent(type, { bubbles: true, clientY: y }));
};

it.each(["pointercancel", "Escape"])("restores a cancelled drag (%s) without saving and removes listeners", async (method) => {
  document.body.innerHTML = `<section data-pinned-root data-pinned-csrf="token">
    <ol data-pinned-list>${["a", "b", "c"].map((key) =>
      `<li data-pinned-item data-pinned-key="${key}"><button data-pinned-handle>${key}</button></li>`
    ).join("")}</ol><p data-pinned-status></p></section>`;
  const fetcher = vi.fn(async () => new Response('{"ok":true}'));
  initPinnedList({ fetch: fetcher });
  const rows = (): string[] => Array.from(document.querySelectorAll<HTMLElement>("[data-pinned-item]"))
    .map((row) => row.dataset.pinnedKey!);
  const handle = document.querySelector<HTMLElement>("[data-pinned-handle]")!;
  const start = (): void => {
    handle.dispatchEvent(new MouseEvent("pointerdown", { bubbles: true, button: 0 }));
    pointer("pointermove");
  };
  start();
  expect(rows()).toEqual(["b", "c", "a"]);
  if (method === "Escape") {
    window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", cancelable: true }));
  } else pointer(method);
  expect(rows()).toEqual(["a", "b", "c"]);
  expect(document.querySelector(".is-dragging, .is-drop-target")).toBeNull();
  expect(document.querySelector("[data-pinned-status]")!.textContent).toBe("Pin reorder cancelled.");
  pointer("pointermove");
  pointer("pointerup");
  await Promise.resolve();
  expect(rows()).toEqual(["a", "b", "c"]);
  expect(fetcher).not.toHaveBeenCalled();
  // Cancellation must not disable subsequent ordinary drags.
  start();
  pointer("pointerup");
  await vi.waitFor(() => expect(fetcher).toHaveBeenCalledTimes(1));
  expect(rows()).toEqual(["b", "c", "a"]);
});
