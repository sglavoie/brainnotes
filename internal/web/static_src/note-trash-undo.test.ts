// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { initTrashUndo, rememberTrashUndo } from "./note-trash-undo";
import { initTrashNoteAction } from "./note-trash";

const notice = { token: "server-one", journalID: "trash-entry", path: "/n/Notes/Gamma" };
const mount = (token = notice.token): void => {
  document.body.innerHTML = `<div data-trash-undo-host data-trash-undo-csrf="${token}"></div>`;
};

afterEach(() => {
  vi.restoreAllMocks();
  window.sessionStorage.clear();
  document.body.innerHTML = "";
});

it("carries the exact trash entry across navigation and restores its note", async () => {
  expect(rememberTrashUndo(notice)).toBe(true);
  mount();
  const fetcher = vi.fn(async () => new Response(JSON.stringify({ ok: true })));
  const redirect = vi.fn();
  initTrashUndo({ fetch: fetcher, redirect });
  document.querySelector<HTMLButtonElement>("[data-property-toast-undo]")!.click();
  await vi.waitFor(() => expect(redirect).toHaveBeenCalledWith(notice.path));
  expect(fetcher.mock.calls[0]).toEqual(["/api/undo", expect.objectContaining({
    body: "token=server-one&id=trash-entry",
  })]);
  // Reloading cannot re-offer the consumed notice.
  mount();
  initTrashUndo({ fetch: fetcher, redirect });
  expect(document.querySelector("[data-trash-undo-toast]")).toBeNull();
});

it("discards notices from another server without offering or issuing an undo", () => {
  rememberTrashUndo(notice);
  mount("server-two");
  const fetcher = vi.fn();
  initTrashUndo({ fetch: fetcher });
  expect(document.querySelector("[data-trash-undo-toast]")).toBeNull();
  expect(fetcher).not.toHaveBeenCalled();
});

it("keeps undo conflicts visible with a history link and never forces restoration", async () => {
  rememberTrashUndo(notice);
  mount();
  const fetcher = vi.fn(async () => new Response(JSON.stringify({ message: "Note changed since trash." }), { status: 409 }));
  const redirect = vi.fn();
  initTrashUndo({ fetch: fetcher, redirect });
  document.querySelector<HTMLButtonElement>("[data-property-toast-undo]")!.click();
  await vi.waitFor(() => expect(document.body.textContent).toContain("Note changed since trash."));
  expect(redirect).not.toHaveBeenCalled();
  expect(fetcher).toHaveBeenCalledTimes(1);
  expect(document.querySelector("[data-property-toast-history]")?.getAttribute("href")).toBe("/history");
});

it("handles blocked storage and malformed notices without breaking the page", () => {
  mount();
  window.sessionStorage.setItem("bn.trash-undo.v1", "invalid JSON");
  expect(() => initTrashUndo()).not.toThrow();
  vi.spyOn(window, "sessionStorage", "get").mockImplementation(() => { throw new Error("blocked"); });
  expect(rememberTrashUndo(notice)).toBe(false);
});

it("never turns an empty stored entry ID into an undo of the latest change", () => {
  rememberTrashUndo({ ...notice, journalID: "   " });
  mount();
  initTrashUndo();
  expect(document.querySelector("[data-trash-undo-toast]")).toBeNull();
});

it("keeps Undo on the note page if storage is blocked", async () => {
  document.body.innerHTML = `<div data-trash-note data-trash-key="Gamma" data-trash-csrf="server-one">
    <button data-trash-button>Trash</button><p data-trash-status></p></div>`;
  vi.spyOn(window, "sessionStorage", "get").mockImplementation(() => { throw new Error("blocked"); });
  const fetcher = vi.fn(async () => new Response(JSON.stringify({ ok: true, journal_id: "trash-entry" }), {
    headers: { "Content-Type": "application/json" },
  }));
  const redirect = vi.fn();
  initTrashNoteAction({ fetch: fetcher, redirect, confirm: () => true });
  document.querySelector<HTMLButtonElement>("[data-trash-button]")!.click();
  await vi.waitFor(() => expect(document.querySelector("[data-trash-undo-toast]")).not.toBeNull());
  expect(redirect).not.toHaveBeenCalled();
  document.querySelector<HTMLButtonElement>("[data-property-toast-undo]")!.click();
  await vi.waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2));
  expect(fetcher.mock.calls[1]).toEqual(["/api/undo", expect.objectContaining({
    body: "token=server-one&id=trash-entry",
  })]);
  await vi.waitFor(() => expect(redirect).toHaveBeenCalled());
});
