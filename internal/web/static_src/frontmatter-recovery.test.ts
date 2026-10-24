// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { initFrontmatterEditing } from "./frontmatter-edit";

const mount = (fetcher: typeof fetch) => {
  window.history.replaceState(null, "", "/n/Example?return_to=%2Flibrary%3Ftag%3Dwork#section");
  document.body.innerHTML = `
    <section data-frontmatter-panel data-frontmatter-key="Example" data-frontmatter-csrf="token">
      <p data-frontmatter-status role="status"></p>
      <form data-frontmatter-set data-frontmatter-property="status">
        <input name="value" value="done"><button>Set</button>
        <input name="locked" value="unchanged" disabled>
      </form>
      <form data-frontmatter-unset data-frontmatter-property="tags"><button>Unset</button></form>
    </section>`;
  initFrontmatterEditing({ fetch: fetcher });
  return {
    forms: Array.from(document.querySelectorAll("form")),
    status: document.querySelector<HTMLElement>("[data-frontmatter-status]")!,
    input: document.querySelector<HTMLInputElement>('[name="value"]')!,
  };
};
const submit = (form: HTMLFormElement): void => { form.dispatchEvent(new Event("submit", { cancelable: true })); };
const json = (body: unknown, status = 200): Response => new Response(JSON.stringify(body), {
  status, headers: { "Content-Type": "application/json" },
});

afterEach(() => {
  document.body.innerHTML = "";
  window.history.replaceState(null, "", "/");
});

it.each(["Updated property.", "Property unchanged."])("confirms %s and offers an explicit refresh", async (message) => {
  const fetcher = vi.fn(async () => json({ ok: true, message }));
  const ui = mount(fetcher);
  submit(ui.forms[0]);
  await vi.waitFor(() => expect(ui.status.classList.contains("is-success")).toBe(true));
  expect(ui.status.textContent).toBe(`${message} Refresh properties`);
  expect(ui.status.querySelector("a")?.href).toBe(window.location.href);
  expect(ui.input.value).toBe("done");
  expect(fetcher).toHaveBeenCalledTimes(1);
});

it.each([0, 1])("does not report a refused operation as successful (form %i)", async (index) => {
  const ui = mount(vi.fn(async () => json({ ok: false, message: "Change refused." })));
  submit(ui.forms[index]);
  await vi.waitFor(() => expect(ui.status.textContent).toBe("Change refused."));
  expect(ui.status.classList.contains("is-error")).toBe(true);
  expect(ui.status.querySelector("a")).toBeNull();
  expect(ui.input.value).toBe("done");
});

it.each([
  () => json({ message: "Not a confirmation" }),
  () => json({ ok: "true" }),
  () => json(null),
  () => new Response("<html>Oops</html>", { headers: { "Content-Type": "text/html" } }),
  () => new Response("<html>Bad gateway</html>", { status: 502, headers: { "Content-Type": "text/html" } }),
  () => new Response("{", { headers: { "Content-Type": "application/json" } }),
  () => { throw new Error("lost connection after write"); },
])("offers verification for an uncertain write", async (response) => {
  const fetcher = vi.fn(async () => response());
  const ui = mount(fetcher);
  submit(ui.forms[0]);
  await vi.waitFor(() => expect(ui.status.textContent).toContain("Change not confirmed."));
  expect(ui.status.classList.contains("is-success")).toBe(false);
  expect(ui.status.querySelector("a")?.textContent).toBe("Refresh to check");
  expect(ui.input.value).toBe("done");
  expect(ui.input.disabled).toBe(false);
  expect(fetcher).toHaveBeenCalledTimes(1);
});

it("blocks repeated and cross-form submissions until completion, restoring disabled states", async () => {
  let finish!: (response: Response) => void;
  const fetcher = vi.fn(() => new Promise<Response>((resolve) => { finish = resolve; }));
  const ui = mount(fetcher);
  submit(ui.forms[0]);
  submit(ui.forms[0]);
  submit(ui.forms[1]);
  expect(fetcher).toHaveBeenCalledTimes(1);
  expect(Array.from(document.querySelectorAll("input, button")).every((control) => (control as HTMLInputElement).disabled)).toBe(true);
  finish(json({ ok: false, message: "Conflict" }, 409));
  await vi.waitFor(() => expect(ui.input.disabled).toBe(false));
  expect(ui.forms[1].querySelector("button")?.disabled).toBe(false);
  expect(document.querySelector<HTMLInputElement>('[name="locked"]')?.disabled).toBe(true);
  expect(ui.input.value).toBe("done");
  submit(ui.forms[1]);
  expect(fetcher).toHaveBeenCalledTimes(2);
  finish(json({ ok: true }));
  await vi.waitFor(() => expect(ui.status.textContent).toBe("Removed property. Refresh properties"));
});
