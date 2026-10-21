// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import { captureFocusReturn, focusedWithin } from "./focus-return";
import { initFrontmatterEditing } from "./frontmatter-edit";
import { initNoteInsight } from "./note-insight";
import { initOverviewSearchResults } from "./overview-search";
import { renderOverviewBulkFixture, selectOverviewResult } from "./search-rendering.test-helpers";
import { searchResult, setURL } from "./search-test-helpers";
import { jsonResponse, waitForExpect } from "./shared.test-helpers";

afterEach(() => {
  vi.useRealTimers();
  document.body.innerHTML = "";
  setURL("/");
});

// A browser moves focus off a control the moment it is disabled; jsdom does
// not, so the tests blur it the way the browser would.
const blurDisabled = (): void => {
  const active = document.activeElement;
  if (active instanceof HTMLElement && (active as HTMLButtonElement).disabled) active.blur();
};

type Gate = { promise: Promise<Response>; open: (response: Response) => void };
const gate = (): Gate => {
  let open: (response: Response) => void = () => {};
  const promise = new Promise<Response>((resolve) => {
    open = resolve;
  });
  return { promise, open };
};

describe("captureFocusReturn", () => {
  const setup = () => {
    document.body.innerHTML = `<input data-a><button data-b>Go</button><p data-text>text</p><input data-c>`;
    const input = document.querySelector("[data-a]") as HTMLInputElement;
    const button = document.querySelector("[data-b]") as HTMLButtonElement;
    return { input, button };
  };

  it("returns focus to the target after the control was blurred", () => {
    const { input, button } = setup();
    input.focus();
    const release = captureFocusReturn(input, [button]);
    input.blur();
    button.dispatchEvent(new Event("pointerdown", { bubbles: true }));
    release();
    expect(document.activeElement).toBe(input);
  });

  it("leaves focus alone after interaction elsewhere, including plain text", () => {
    const { input } = setup();
    input.focus();
    const release = captureFocusReturn(input);
    input.blur();
    document.querySelector("[data-text]")?.dispatchEvent(new Event("pointerdown", { bubbles: true }));
    release();
    expect(document.activeElement).toBe(document.body);

    const other = document.querySelector("[data-c]") as HTMLInputElement;
    input.focus();
    const again = captureFocusReturn(input);
    other.focus();
    again();
    expect(document.activeElement).toBe(other);
  });

  it("does nothing without a target, or for a removed one", () => {
    const { input } = setup();
    expect(() => captureFocusReturn(null)()).not.toThrow();
    input.focus();
    const release = captureFocusReturn(input);
    input.remove();
    release();
    expect(document.activeElement).toBe(document.body);
  });

  it("finds the focused element only within its scope", () => {
    const { input, button } = setup();
    input.focus();
    expect(focusedWithin(document.body)).toBe(input);
    expect(focusedWithin(button)).toBeNull();
    expect(focusedWithin(null)).toBeNull();
  });
});

describe("focus after async submits", () => {
  it("frontmatter: returns focus to the Set button once the write settles", async () => {
    document.body.innerHTML = `
      <section data-frontmatter-panel data-frontmatter-key="source" data-frontmatter-csrf="csrf-token">
        <p data-frontmatter-status> </p>
        <form data-frontmatter-set data-frontmatter-property="status" data-frontmatter-op="=">
          <input name="value" value="done" />
          <button type="submit">Set</button>
        </form>
      </section>`;
    const reply = gate();
    initFrontmatterEditing({ fetch: (() => reply.promise) as typeof fetch });
    const button = document.querySelector("button") as HTMLButtonElement;
    button.focus();
    document.querySelector("form")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    expect(button.disabled).toBe(true);
    blurDisabled();
    reply.open(jsonResponse({ ok: true, message: "Updated frontmatter." }));
    await waitForExpect(() => expect(button.disabled).toBe(false));
    expect(document.activeElement).toBe(button);
  });

  it("note insight: returns focus to the prompt after the response", async () => {
    document.body.innerHTML = `
      <section data-note-insight data-note-insight-endpoint="/api/note/insight"
        data-note-insight-key="notes/source" data-note-insight-csrf="csrf-token" data-note-insight-enabled="true">
        <textarea data-note-insight-prompt></textarea>
        <button type="button" data-note-insight-submit>Ask AI</button>
        <p data-note-insight-status></p>
        <section data-note-insight-result hidden>
          <button type="button" data-note-insight-copy disabled>Copy Markdown</button>
          <div data-note-insight-body></div>
        </section>
      </section>`;
    const reply = gate();
    initNoteInsight({ fetch: (() => reply.promise) as typeof fetch, confirm: () => true });
    const prompt = document.querySelector("[data-note-insight-prompt]") as HTMLTextAreaElement;
    prompt.focus();
    prompt.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", ctrlKey: true, bubbles: true }));
    await waitForExpect(() => expect(prompt.disabled).toBe(true));
    blurDisabled();
    reply.open(jsonResponse({ ok: true, enabled: true, key: "notes/source", output: "x", rendered_html: "<p>x</p>" }));
    await waitForExpect(() => expect(prompt.disabled).toBe(false));
    expect(document.activeElement).toBe(prompt);
  });

  it("bulk: returns focus to the Preview button after the preview", async () => {
    vi.useFakeTimers();
    setURL("/?q=source");
    renderOverviewBulkFixture();
    const reply = gate();
    const fetchMock = vi.fn(async (input: RequestInfo | URL) =>
      String(input) === "/api/notes/bulk/preview"
        ? reply.promise
        : jsonResponse([searchResult({ key: "source", path: "/n/source", title: "Source" })])
    );
    initOverviewSearchResults({ fetch: fetchMock });
    await vi.runAllTimersAsync();
    selectOverviewResult();
    const trash = document.querySelector("[data-bulk-trash]") as HTMLButtonElement;
    trash.focus();
    trash.click();
    expect(trash.disabled).toBe(true);
    blurDisabled();
    reply.open(jsonResponse({
      ok: true, action: "trash", message: "Preview: 1 note would be moved to Trash.", changed: 1,
      preview_id: "preview-trash", expires_at: "2026-06-02T16:34:56Z",
    }));
    await vi.runAllTimersAsync();
    expect(trash.disabled).toBe(false);
    expect(document.activeElement).toBe(trash);
  });
});
