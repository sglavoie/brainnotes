// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { initCapture } from "./capture";

const panel = (vault = "vault-a", target = "Inbox.md", heading = "## Captures") => {
  document.body.innerHTML = `
    <section data-capture-root data-capture-configured="true" data-capture-csrf="token">
      <textarea data-capture-input></textarea><button data-capture-submit>Capture</button>
      <p data-capture-status></p><p data-capture-draft-status hidden></p>
      <a data-capture-view-inbox hidden>View inbox</a><ul data-capture-recent></ul>
    </section>`;
  const root = document.querySelector<HTMLElement>("section")!;
  Object.assign(root.dataset, { captureVault: vault, captureTarget: target, captureHeading: heading });
  return {
    input: root.querySelector("textarea")!,
    submit: root.querySelector("button")!,
    link: root.querySelector("a")!,
    draftStatus: root.querySelector<HTMLElement>("[data-capture-draft-status]")!,
  };
};
const type = (input: HTMLTextAreaElement, text: string): void => {
  input.value = text;
  input.dispatchEvent(new Event("input"));
};

afterEach(() => {
  vi.restoreAllMocks();
  sessionStorage.clear();
  document.body.innerHTML = "";
});

it("restores unfinished text after navigation without submitting it", () => {
  const fetcher = vi.fn();
  let view = panel();
  initCapture({ fetch: fetcher });
  type(view.input, "ask [[Bob]]\nabout the plan");
  view = panel();
  initCapture({ fetch: fetcher });
  expect(view.input.value).toBe("ask [[Bob]]\nabout the plan");
  expect(view.draftStatus.textContent).toContain("restored");
  expect(fetcher).not.toHaveBeenCalled();
});

it.each([
  ["vault-b", "Inbox.md", "## Captures"],
  ["vault-a", "Other.md", "## Captures"],
  ["vault-a", "Inbox.md", "## Other"],
])("keeps drafts separate for %s / %s / %s", (vault, target, heading) => {
  const original = panel();
  initCapture();
  type(original.input, "belongs to the original destination");
  const other = panel(vault, target, heading);
  initCapture();
  expect(other.input.value).toBe("");
  const restored = panel();
  initCapture();
  expect(restored.input.value).toBe("belongs to the original destination");
});

it("clears the draft and exposes the server's inbox link only after success", async () => {
  const view = panel();
  initCapture({ fetch: vi.fn(async () => new Response(JSON.stringify({
    ok: true, path: "/n/nested/inbox%20%23", detail: "- saved thought",
  }), { headers: { "Content-Type": "application/json" } })) });
  type(view.input, "thought");
  expect(view.link.hidden).toBe(true);
  view.submit.click();
  await vi.waitFor(() => expect(view.submit.disabled).toBe(false));
  expect(view.link.hidden).toBe(false);
  expect(view.link.getAttribute("href")).toBe("/n/nested/inbox%20%23");
  const restored = panel();
  initCapture();
  expect(restored.input.value).toBe("");
});

it.each(["refused", "network"])("retains the draft after a %s failure", async (failure) => {
  const view = panel();
  initCapture({ fetch: vi.fn(async () => {
    if (failure === "network") { throw new Error("offline"); }
    return new Response(JSON.stringify({ ok: false, message: "Conflict" }), { status: 409, headers: { "Content-Type": "application/json" } });
  }) });
  type(view.input, "keep this");
  view.submit.click();
  await vi.waitFor(() => expect(view.submit.disabled).toBe(false));
  expect(view.link.hidden).toBe(true);
  const restored = panel();
  initCapture();
  expect(restored.input.value).toBe("keep this");
});

it("remembers an explicitly cleared draft", () => {
  const view = panel();
  initCapture();
  type(view.input, "discard this");
  type(view.input, "");
  const restored = panel();
  initCapture();
  expect(restored.input.value).toBe("");
});

it("still captures when browser storage is blocked", async () => {
  vi.spyOn(window, "sessionStorage", "get").mockImplementation(() => { throw new Error("blocked"); });
  const view = panel();
  initCapture({ fetch: vi.fn(async () => new Response(JSON.stringify({ ok: true }), { headers: { "Content-Type": "application/json" } })) });
  type(view.input, "capture without storage");
  expect(view.draftStatus.textContent).toContain("could not keep");
  view.submit.click();
  await vi.waitFor(() => expect(view.submit.disabled).toBe(false));
  expect(view.input.value).toBe("");
});
