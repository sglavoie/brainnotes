// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { initTrashNoteAction } from "./note-trash";

const mount = (fetcher: typeof fetch, confirm: () => boolean | Promise<boolean> = () => true) => {
  document.body.innerHTML = '<div data-trash-note data-trash-key="Notes/Example" data-trash-csrf="fixture"><button data-trash-button>Trash</button><p data-trash-status></p></div>';
  const redirect = vi.fn();
  initTrashNoteAction({ fetch: fetcher, confirm, redirect });
  const button = document.querySelector<HTMLButtonElement>("button")!;
  const status = document.querySelector<HTMLElement>("[data-trash-status]")!;
  return { button, status, redirect };
};
afterEach(() => { document.body.replaceChildren(); vi.restoreAllMocks(); });

it.each(["HTML", "truncated JSON", "malformed", "redirect", "network"])("keeps the note and reports an unconfirmed trash after %s", async (kind) => {
  const confirmed = vi.fn();
  window.addEventListener("bn:writeconfirmed", confirmed, { once: true });
  const fetcher = vi.fn(async () => {
    if (kind === "network") throw new TypeError("connection lost");
    const response = new Response(kind === "HTML" ? "<html>Sign in</html>" : kind === "truncated JSON" ? '{"ok":' : '{}', {
      status: 200, headers: { "Content-Type": kind === "HTML" ? "text/html" : "application/json" },
    });
    if (kind === "redirect") Object.defineProperty(response, "redirected", { value: true });
    return response;
  });
  const ui = mount(fetcher);
  ui.button.click();
  await vi.waitFor(() => expect(ui.status.textContent).toContain("Change not confirmed"));
  expect(ui.status.classList.contains("is-success")).toBe(false);
  expect(ui.redirect).not.toHaveBeenCalled();
  expect(confirmed).not.toHaveBeenCalled();
  expect(fetcher).toHaveBeenCalledTimes(1);
  expect(ui.button.disabled).toBe(false);
  window.removeEventListener("bn:writeconfirmed", confirmed);
});

it("treats a successful HTTP response carrying a refusal as refused", async () => {
  const ui = mount(vi.fn(async () => Response.json({ ok: false, message: "The note changed." })));
  ui.button.click();
  await vi.waitFor(() => expect(ui.status.textContent).toBe("The note changed."));
  expect(ui.redirect).not.toHaveBeenCalled();
});

it("waits for confirmation and blocks repeated submissions while the dialog is pending", async () => {
  let answer!: (confirmed: boolean) => void;
  const fetcher = vi.fn(async () => Response.json({ ok: true }));
  const confirm = vi.fn(() => new Promise<boolean>((resolve) => { answer = resolve; }));
  const ui = mount(fetcher, confirm);
  ui.button.click();
  ui.button.click();
  expect(confirm).toHaveBeenCalledTimes(1);
  expect(fetcher).not.toHaveBeenCalled();
  answer(false);
  await vi.waitFor(() => expect(ui.status.textContent).toBe("Trash canceled."));
  expect(fetcher).not.toHaveBeenCalled();
  expect(ui.button.disabled).toBe(false);
});
