// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import {
  addCandidate,
  jsonResponse,
  memoryStorage,
  query,
  relatedPayload,
  routedFetch,
  selectedKeys,
  start,
  workbench,
} from "./synthesize.test-helpers";

afterEach(() => {
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

describe("synthesis context packet", () => {
  const prepared = async (
    handlers: Parameters<typeof routedFetch>[0] = {}
  ): Promise<ReturnType<typeof vi.fn>> => {
    const fetcher = routedFetch({
      related: () => jsonResponse(relatedPayload("balanced", ["one", "two"])),
      ...handlers,
    });
    await start({
      fetch: fetcher,
      storage: memoryStorage(),
      clipboard: { writeText: vi.fn(async () => undefined) },
    });
    addCandidate("one");
    query<HTMLButtonElement>("[data-synthesize-prepare]").click();
    await vi.waitFor(() => {
      expect(query<HTMLElement>("[data-synthesize-packet]").textContent).not.toBe("");
    });
    return fetcher;
  };

  it("posts the ordered selection and reports bytes, notes, and excerpt locations", async () => {
    const fetcher = await prepared();
    const contextCall = fetcher.mock.calls.find(
      (call) => String(call[0]) === "/api/synthesis/context"
    );
    const body = new URLSearchParams(String((contextCall?.[1] as RequestInit).body));
    expect(body.getAll("candidate")).toEqual(["one"]);
    expect(body.get("source")).toBe("anchor");
    expect(body.get("token")).toBe("csrf-token");

    expect(query<HTMLElement>("[data-synthesize-context-summary]").textContent).toBe(
      "2 notes, 2048 bytes (2.0 KiB) of a 40960 bytes (40.0 KiB) budget, purpose Synthesis."
    );
    expect(
      Array.from(
        query<HTMLElement>("[data-synthesize-manifest]").querySelectorAll(
          ".synthesize-manifest-excerpts code"
        )
      ).map((code) => code.textContent)
    ).toEqual(["Notes/one.md:4-9"]);
    expect(query<HTMLElement>("[data-synthesize-packet]").textContent).toBe(
      "# Packet\n\nexact bytes\n"
    );
  });

  it("invalidates the packet when the selection, order, purpose, or prompt changes", async () => {
    await prepared();
    const prepare = query<HTMLButtonElement>("[data-synthesize-prepare]");
    expect(prepare.textContent).toBe("Rebuild context");

    addCandidate("two");
    expect(query<HTMLElement>("[data-synthesize-packet]").textContent).toBe("");
    expect(query<HTMLButtonElement>("[data-synthesize-copy]").disabled).toBe(true);

    prepare.click();
    await vi.waitFor(() => {
      expect(query<HTMLElement>("[data-synthesize-packet]").textContent).not.toBe("");
    });
    const purpose = query<HTMLSelectElement>("[data-synthesize-purpose]");
    purpose.value = "questions";
    purpose.dispatchEvent(new Event("change", { bubbles: true }));
    expect(query<HTMLElement>("[data-synthesize-packet]").textContent).toBe("");

    prepare.click();
    await vi.waitFor(() => {
      expect(query<HTMLElement>("[data-synthesize-packet]").textContent).not.toBe("");
    });
    const prompt = query<HTMLTextAreaElement>("[data-synthesize-prompt]");
    prompt.value = "one more angle";
    prompt.dispatchEvent(new Event("input", { bubbles: true }));
    expect(query<HTMLElement>("[data-synthesize-packet]").textContent).toBe("");
  });

  it("keeps the packet when only the draft, title, or folder changes", async () => {
    await prepared();
    for (const selector of [
      "[data-synthesize-title]",
      "[data-synthesize-folder]",
      "[data-synthesize-draft]",
    ]) {
      const field = query<HTMLInputElement | HTMLTextAreaElement>(selector);
      field.value = "changed";
      field.dispatchEvent(new Event("input", { bubbles: true }));
    }
    expect(query<HTMLElement>("[data-synthesize-packet]").textContent).toBe(
      "# Packet\n\nexact bytes\n"
    );
    expect(query<HTMLButtonElement>("[data-synthesize-copy]").disabled).toBe(false);
  });

  it("copies the exact packet bytes and reports a clipboard refusal", async () => {
    const writeText = vi
      .fn<(text: string) => Promise<void>>()
      .mockResolvedValueOnce(undefined)
      .mockRejectedValueOnce(new Error("denied"));
    const fetcher = routedFetch({
      related: () => jsonResponse(relatedPayload("balanced", ["one"])),
    });
    await start({ fetch: fetcher, storage: memoryStorage(), clipboard: { writeText } });
    addCandidate("one");
    query<HTMLButtonElement>("[data-synthesize-prepare]").click();
    await vi.waitFor(() => {
      expect(query<HTMLButtonElement>("[data-synthesize-copy]").disabled).toBe(false);
    });

    query<HTMLButtonElement>("[data-synthesize-copy]").click();
    await vi.waitFor(() => {
      expect(query<HTMLElement>("[data-synthesize-context-status]").textContent).toBe(
        "Context packet copied."
      );
    });
    expect(writeText).toHaveBeenCalledWith("# Packet\n\nexact bytes\n");

    query<HTMLButtonElement>("[data-synthesize-copy]").click();
    await vi.waitFor(() => {
      expect(query<HTMLElement>("[data-synthesize-context-status]").textContent).toContain(
        "copy it manually"
      );
    });
  });

  it("disables copying when the browser has no writable clipboard", async () => {
    const fetcher = routedFetch({
      related: () => jsonResponse(relatedPayload("balanced", ["one"])),
    });
    await start({ fetch: fetcher, storage: memoryStorage(), clipboard: null });
    addCandidate("one");
    query<HTMLButtonElement>("[data-synthesize-prepare]").click();
    await vi.waitFor(() => {
      expect(query<HTMLElement>("[data-synthesize-packet]").textContent).not.toBe("");
    });
    expect(query<HTMLButtonElement>("[data-synthesize-copy]").disabled).toBe(true);
  });

  it("keeps the workbench usable when the context request is refused", async () => {
    const fetcher = routedFetch({
      related: () => jsonResponse(relatedPayload("balanced", ["one"])),
      context: () =>
        jsonResponse({ ok: false, code: "conflict", message: "A source changed." }, 409),
    });
    await start({ fetch: fetcher, storage: memoryStorage(), clipboard: null });
    addCandidate("one");
    query<HTMLButtonElement>("[data-synthesize-prepare]").click();
    await vi.waitFor(() => {
      expect(query<HTMLElement>("[data-synthesize-context-status]").textContent).toBe(
        "A source changed."
      );
    });
    expect(query<HTMLButtonElement>("[data-synthesize-prepare]").disabled).toBe(false);
    expect(selectedKeys()).toEqual(["anchor", "one"]);
  });
});
