// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import { buildNoteInsightForm, normalizeNoteInsightResponse } from "./api";
import { initNoteInsight } from "./note-insight";

const waitForExpect = async (assertion: () => void): Promise<void> => {
  let lastError: unknown;
  for (let attempt = 0; attempt < 20; attempt += 1) {
    try {
      assertion();
      return;
    } catch (error) {
      lastError = error;
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
  }
  throw lastError;
};

const fixture = (enabled = true): string => `
  <section
    data-note-insight
    data-note-insight-endpoint="/api/note/insight"
    data-note-insight-key="notes/source"
    data-note-insight-csrf="csrf-token"
    data-note-insight-enabled="${enabled}"
  >
    <textarea data-note-insight-prompt ${enabled ? "" : "disabled"}></textarea>
    <button type="button" data-note-insight-submit ${enabled ? "" : "disabled"}>Ask AI</button>
    <p data-note-insight-status></p>
    <section data-note-insight-result hidden>
      <button type="button" data-note-insight-copy disabled>Copy Markdown</button>
      <div data-note-insight-body></div>
    </section>
  </section>
`;

const jsonResponse = (body: unknown, status = 200): Response =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

afterEach(() => {
  document.body.innerHTML = "";
});

describe("note insight API helpers", () => {
  it("normalizes responses and trims optional prompts", () => {
    expect(normalizeNoteInsightResponse({
      ok: true,
      enabled: "true",
      key: "notes/source",
      output: "raw",
      rendered_html: "<p>safe</p>",
    })).toMatchObject({
      ok: true,
      enabled: true,
      key: "notes/source",
      output: "raw",
      rendered_html: "<p>safe</p>",
    });
    expect(buildNoteInsightForm({ key: "notes/source", prompt: "  Compare options  ", token: "csrf" }).toString()).toBe(
      "key=notes%2Fsource&prompt=Compare+options&token=csrf"
    );
    expect(buildNoteInsightForm({ key: "notes/source", prompt: " ", token: "csrf" }).toString()).toBe(
      "key=notes%2Fsource&token=csrf"
    );
  });
});

describe("note insight controller", () => {
  it("sends the custom prompt, renders safe server HTML, and copies raw Markdown", async () => {
    document.body.innerHTML = fixture();
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) =>
      jsonResponse({
        ok: true,
        enabled: true,
        key: "notes/source",
        output: "**Answer**\n<script>alert(1)</script>",
        rendered_html: "<p><strong>Answer</strong></p><p>raw HTML omitted</p>",
      })
    );
    const confirmMock = vi.fn(() => true);
    const clipboardMock = vi.fn(async () => {});
    initNoteInsight({
      fetch: fetchMock as typeof fetch,
      confirm: confirmMock,
      writeClipboard: clipboardMock,
    });

    const prompt = document.querySelector<HTMLTextAreaElement>("[data-note-insight-prompt]");
    expect(prompt).not.toBeNull();
    prompt!.value = "Give me three next steps";
    document.querySelector<HTMLButtonElement>("[data-note-insight-submit]")?.click();

    await waitForExpect(() => {
      expect(document.querySelector("[data-note-insight-result]")?.hasAttribute("hidden")).toBe(false);
    });
    const [, request] = fetchMock.mock.calls[0];
    expect(String(request?.body)).toContain("prompt=Give+me+three+next+steps");
    expect(confirmMock).toHaveBeenCalledTimes(1);
    expect(document.querySelector("[data-note-insight-body]")?.innerHTML).toContain("<strong>Answer</strong>");
    expect(document.querySelector("[data-note-insight-body]")?.innerHTML).not.toContain("<script>");

    document.querySelector<HTMLButtonElement>("[data-note-insight-copy]")?.click();
    await waitForExpect(() => {
      expect(clipboardMock).toHaveBeenCalledWith("**Answer**\n<script>alert(1)</script>");
    });
  });

  it("submits with Ctrl+Enter and keeps disabled configurations inert", async () => {
    document.body.innerHTML = fixture();
    const fetchMock = vi.fn(async () => jsonResponse({ ok: true, enabled: true, key: "notes/source" }));
    initNoteInsight({ fetch: fetchMock as typeof fetch, confirm: () => true });
    document.querySelector<HTMLTextAreaElement>("[data-note-insight-prompt]")?.dispatchEvent(
      new KeyboardEvent("keydown", { bubbles: true, ctrlKey: true, key: "Enter" })
    );
    await waitForExpect(() => expect(fetchMock).toHaveBeenCalledTimes(1));

    document.body.innerHTML = fixture(false);
    const disabledFetch = vi.fn(async () => jsonResponse({ ok: true }));
    initNoteInsight({ fetch: disabledFetch as typeof fetch, confirm: () => true });
    document.querySelector<HTMLButtonElement>("[data-note-insight-submit]")?.click();
    expect(disabledFetch).not.toHaveBeenCalled();
  });
});
