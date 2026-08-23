// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import {
  addCandidate,
  contextPayload,
  deferredResponse,
  jsonResponse,
  memoryStorage,
  prepareContext,
  previewPayload,
  query,
  relatedPayload,
  routedFetch,
  settle,
  start,
  typeInto,
} from "./synthesize.test-helpers";

afterEach(() => {
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

describe("synthesis in-flight request versioning", () => {
  const changePurpose = (): void => {
    const purpose = query<HTMLSelectElement>("[data-synthesize-purpose]");
    purpose.value = "questions";
    purpose.dispatchEvent(new Event("change", { bubbles: true }));
  };

  const withPendingContext = async (pending: Promise<Response>): Promise<void> => {
    const fetcher = routedFetch({
      related: () => jsonResponse(relatedPayload("balanced", ["one", "two"])),
      context: () => pending,
    });
    await start({ fetch: fetcher, storage: memoryStorage(), clipboard: null });
    addCandidate("one");
    query<HTMLButtonElement>("[data-synthesize-prepare]").click();
  };

  const expectNoContext = (): void => {
    expect(query<HTMLElement>("[data-synthesize-packet]").textContent).toBe("");
    expect(query<HTMLButtonElement>("[data-synthesize-generate]").disabled).toBe(true);
    expect(query<HTMLButtonElement>("[data-synthesize-copy]").disabled).toBe(true);
    expect(query<HTMLElement>("[data-synthesize-context-status]").textContent).toContain(
      "Prepare the context again."
    );
  };

  const contextChanges: [string, () => void][] = [
    ["the selection", () => addCandidate("two")],
    ["the purpose", changePurpose],
    ["the prompt", () => typeInto("[data-synthesize-prompt]", "one more angle")],
  ];

  for (const [name, change] of contextChanges) {
    it(`refuses a context packet built before ${name} changed`, async () => {
      const pending = deferredResponse();
      await withPendingContext(pending.promise);
      change();
      pending.release(jsonResponse(contextPayload(["one"])));
      await settle();
      expectNoContext();
    });
  }

  it("does not blame the current selection for a failure of a superseded context request", async () => {
    const pending = deferredResponse();
    await withPendingContext(pending.promise);
    addCandidate("two");
    pending.release(
      jsonResponse({ ok: false, code: "conflict", message: "A source changed." }, 409)
    );
    await settle();
    expect(query<HTMLElement>("[data-synthesize-context-status]").textContent).not.toContain(
      "A source changed."
    );
    expectNoContext();
  });

  const stalePreview = (): Response =>
    jsonResponse(
      previewPayload(
        new URLSearchParams({
          context_id: "ctx-1",
          title: "Result",
          folder: "Synthesis",
          draft: "what these add up to",
        })
      )
    );

  const withPendingPreview = async (pending: Promise<Response>): Promise<void> => {
    const fetcher = routedFetch({
      related: () => jsonResponse(relatedPayload("balanced", ["one", "two"])),
      preview: () => pending,
    });
    await start({ fetch: fetcher, storage: memoryStorage(), clipboard: null });
    addCandidate("one");
    await prepareContext();
    typeInto("[data-synthesize-title]", "Result");
    typeInto("[data-synthesize-draft]", "what these add up to");
    query<HTMLButtonElement>("[data-synthesize-preview]").click();
  };

  const expectNoReceipt = (): void => {
    expect(query<HTMLElement>("[data-synthesize-preview-panel]").hidden).toBe(true);
    expect(query<HTMLButtonElement>("[data-synthesize-apply]").disabled).toBe(true);
    expect(query<HTMLElement>("[data-synthesize-preview-status]").textContent).toContain(
      "Preview again."
    );
  };

  const previewChanges: [string, () => void][] = [
    ["the title", () => typeInto("[data-synthesize-title]", "Another title")],
    ["the folder", () => typeInto("[data-synthesize-folder]", "Elsewhere")],
    ["the draft", () => typeInto("[data-synthesize-draft]", "different words")],
    [
      "the context",
      () => query<HTMLButtonElement>("[data-synthesize-remove='one']").click(),
    ],
  ];

  for (const [name, change] of previewChanges) {
    it(`refuses a receipt issued before ${name} changed`, async () => {
      const pending = deferredResponse();
      await withPendingPreview(pending.promise);
      change();
      pending.release(stalePreview());
      await settle();
      expectNoReceipt();
    });
  }

  it("does not resurrect a superseded context when a late preview reports it expired", async () => {
    const pending = deferredResponse();
    await withPendingPreview(pending.promise);
    changePurpose();
    pending.release(
      jsonResponse({ ok: false, code: "not_found", message: "context expired" }, 404)
    );
    await settle();
    expectNoReceipt();
    expect(query<HTMLElement>("[data-synthesize-packet]").textContent).toBe("");
    expect(query<HTMLTextAreaElement>("[data-synthesize-draft]").value).toBe(
      "what these add up to"
    );
  });

  it("still installs a response for inputs the user left alone", async () => {
    const pending = deferredResponse();
    await withPendingPreview(pending.promise);
    pending.release(stalePreview());
    await settle();
    expect(query<HTMLElement>("[data-synthesize-preview-panel]").hidden).toBe(false);
    expect(query<HTMLButtonElement>("[data-synthesize-apply]").disabled).toBe(false);
  });
});
