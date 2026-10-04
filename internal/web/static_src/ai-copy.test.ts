// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { initNoteInsight } from "./note-insight";
import { runCopyInsight, type BriefActionContext } from "./daily-brief-actions";

const markdown = "**Answer**\n<script>not HTML</script>";

afterEach(() => {
  document.body.innerHTML = "";
  vi.unstubAllGlobals();
});

const expectFallback = (): void => {
  const field = document.querySelector<HTMLTextAreaElement>("[data-copy-fallback]")!;
  expect(field.value).toBe(markdown);
  expect(field.readOnly).toBe(true);
  expect(document.activeElement).toBe(field);
  expect(field.selectionStart).toBe(0);
  expect(field.selectionEnd).toBe(markdown.length);
  expect(document.querySelector("script")).toBeNull();
};

it.each([false, true])("Ask AI preserves raw Markdown when clipboard is missing or rejected: %s", async (available) => {
  const writeText = vi.fn(async () => { throw new Error("Denied"); });
  vi.stubGlobal("navigator", { clipboard: available ? { writeText } : undefined });
  document.body.innerHTML = `<section data-note-insight data-note-insight-key="note"
    data-note-insight-enabled="true" data-note-insight-csrf="token">
    <textarea data-note-insight-prompt></textarea><button data-note-insight-submit>Ask</button>
    <p data-note-insight-status></p><section data-note-insight-result hidden>
      <button data-note-insight-copy disabled>Copy</button><div data-note-insight-body></div>
    </section></section>`;
  initNoteInsight({ confirm: () => true, fetch: async () => new Response(JSON.stringify({
    ok: true, enabled: true, key: "note", output: markdown, rendered_html: "<p>Answer</p>",
  }), { headers: { "Content-Type": "application/json" } }) });
  const submit = document.querySelector<HTMLButtonElement>("[data-note-insight-submit]")!;
  const copy = document.querySelector<HTMLButtonElement>("[data-note-insight-copy]")!;
  submit.click();
  await vi.waitFor(() => expect(copy.disabled).toBe(false));
  copy.click();
  await vi.waitFor(expectFallback);
  await vi.waitFor(() => expect(copy.disabled).toBe(false));
  submit.click();
  await vi.waitFor(() => expect(document.querySelector("[data-copy-fallback]")).toBeNull());
});

it.each([false, true])("Daily Brief offers a fallback for missing or rejected clipboard: %s", async (available) => {
  document.body.innerHTML = `<section data-brief-insight-panel>
    <button data-brief-slot="continue">Copy</button></section>`;
  const button = document.querySelector("button")!;
  button.dataset.briefCopyOutput = markdown;
  const writer = vi.fn<NonNullable<BriefActionContext["clipboardWrite"]>>()
    .mockRejectedValueOnce(new Error("Denied")).mockResolvedValue(undefined);
  // These are the only dependencies used by the copy action.
  const ctx = { clipboardWrite: available ? writer : undefined, setSlotStatus: vi.fn() } as unknown as BriefActionContext;
  await runCopyInsight(ctx, button);
  expectFallback();
  expect(button.disabled).toBe(false);
  ctx.clipboardWrite = vi.fn(async () => {});
  await runCopyInsight(ctx, button);
  expect(ctx.clipboardWrite).toHaveBeenCalledWith(markdown);
  expect(document.querySelector("[data-copy-fallback]")).toBeNull();
});
