// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import {
  addCandidate,
  identity,
  jsonResponse,
  memoryStorage,
  query,
  relatedPayload,
  routedFetch,
  selectedKeys,
  start,
  statusText,
  typeInto,
  unclearableStorage,
  workbench,
} from "./synthesize.test-helpers";
import { SYNTHESIS_STORAGE_VERSION, type SynthesisStorageLike } from "./synthesize-storage";

afterEach(() => {
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

describe("synthesis persistence", () => {
  it("saves note keys only and never the packet or excerpt text", async () => {
    const storage = memoryStorage();
    const fetcher = routedFetch({
      related: () => jsonResponse(relatedPayload("balanced", ["one"])),
    });
    await start({ fetch: fetcher, storage, clipboard: null });

    addCandidate("one");
    typeInto("[data-synthesize-title]", "Result note");
    typeInto("[data-synthesize-draft]", "  draft body  ");

    const raw = String(storage.entries.get("bn.synthesize.vault-1.anchor"));
    expect(JSON.parse(raw)).toEqual({
      version: SYNTHESIS_STORAGE_VERSION,
      sources: ["one"],
      title: "Result note",
      folder: "Synthesis",
      purpose: "synthesis",
      prompt: "",
      draft: "  draft body  ",
      candidate: "",
    });
    expect(raw).not.toContain("one excerpt");
  });

  it("rehydrates saved keys from the server and warns about a note the vault lost", async () => {
    const storage = memoryStorage();
    storage.entries.set(
      "bn.synthesize.vault-1.anchor",
      JSON.stringify({
        version: SYNTHESIS_STORAGE_VERSION,
        sources: ["one", "gone", "two"],
        title: "Saved title",
        folder: "Saved/Folder",
        purpose: "questions",
        prompt: "saved prompt",
        draft: "saved draft",
      })
    );
    const fetcher = routedFetch({
      related: (url) => {
        const source = url.searchParams.get("source") || "";
        if (url.searchParams.get("limit") === "1") {
          return source === "gone"
            ? jsonResponse({ ok: false, message: "note not found" }, 404)
            : jsonResponse({ ...relatedPayload("balanced", []), source: identity(source) });
        }
        return jsonResponse(relatedPayload(url.searchParams.get("mode") || "balanced", ["one"]));
      },
    });
    await start({ fetch: fetcher, storage, clipboard: null });

    expect(selectedKeys()).toEqual(["anchor", "one", "two"]);
    expect(query<HTMLInputElement>("[data-synthesize-title]").value).toBe("Saved title");
    expect(query<HTMLInputElement>("[data-synthesize-folder]").value).toBe("Saved/Folder");
    expect(query<HTMLSelectElement>("[data-synthesize-purpose]").value).toBe("questions");
    expect(query<HTMLTextAreaElement>("[data-synthesize-prompt]").value).toBe("saved prompt");
    expect(query<HTMLTextAreaElement>("[data-synthesize-draft]").value).toBe("saved draft");

    const warning = query<HTMLElement>("[data-synthesize-warning]");
    expect(warning.hidden).toBe(false);
    expect(warning.textContent).toContain("gone");
    expect(JSON.parse(String(storage.entries.get("bn.synthesize.vault-1.anchor"))).sources).toEqual([
      "one",
      "two",
    ]);
    expect(query<HTMLButtonElement>("[data-synthesize-add='one']").disabled).toBe(true);
  });

  it("ignores a record written by an older schema", async () => {
    const storage = memoryStorage();
    storage.entries.set(
      "bn.synthesize.vault-1.anchor",
      JSON.stringify({ version: 0, sources: ["one"], title: "Old" })
    );
    await start({ fetch: routedFetch(), storage, clipboard: null });
    expect(selectedKeys()).toEqual(["anchor"]);
    expect(query<HTMLInputElement>("[data-synthesize-title]").value).toBe("");
    expect(query<HTMLElement>("[data-synthesize-warning]").hidden).toBe(true);
  });

  it("warns once when the browser refuses to store anything", async () => {
    const storage: SynthesisStorageLike = {
      getItem: () => null,
      setItem: () => {
        throw new Error("quota exceeded");
      },
      removeItem: () => undefined,
    };
    const fetcher = routedFetch({
      related: () => jsonResponse(relatedPayload("balanced", ["one"])),
    });
    await start({ fetch: fetcher, storage, clipboard: null });
    addCandidate("one");
    const warning = query<HTMLElement>("[data-synthesize-warning]");
    expect(warning.hidden).toBe(false);
    expect(warning.textContent).toContain("refused to save");
  });

  it("restores a saved candidate as Markdown without any stored rendering", async () => {
    const storage = memoryStorage();
    storage.entries.set(
      "bn.synthesize.vault-1.anchor",
      JSON.stringify({
        version: SYNTHESIS_STORAGE_VERSION,
        sources: [],
        title: "Saved title",
        folder: "Synthesis",
        purpose: "synthesis",
        prompt: "",
        draft: "saved draft",
        candidate: "restored candidate body",
      })
    );
    await start({ fetch: routedFetch(), storage, clipboard: null });

    expect(query<HTMLElement>("[data-synthesize-candidate-panel]").hidden).toBe(false);
    expect(query<HTMLElement>("[data-synthesize-candidate-markdown]").textContent).toBe(
      "restored candidate body"
    );
    expect(query<HTMLElement>("[data-synthesize-candidate-rendered]").textContent).toBe(
      "Restored candidate — shown as Markdown."
    );
    expect(query<HTMLTextAreaElement>("[data-synthesize-draft]").value).toBe("saved draft");
  });

  it("clears the saved record and the whole selection on a confirmed discard", async () => {
    const storage = memoryStorage();
    const fetcher = routedFetch({
      related: () => jsonResponse(relatedPayload("balanced", ["one"])),
    });
    const confirm = vi.fn((message: string) => message !== "");
    await start({ fetch: fetcher, storage, clipboard: null, confirm });
    addCandidate("one");
    typeInto("[data-synthesize-draft]", "words");

    query<HTMLButtonElement>("[data-synthesize-discard]").click();
    expect(confirm).toHaveBeenCalledTimes(1);
    expect(storage.entries.has("bn.synthesize.vault-1.anchor")).toBe(false);
    expect(selectedKeys()).toEqual(["anchor"]);
    expect(query<HTMLTextAreaElement>("[data-synthesize-draft]").value).toBe("");
    expect(query<HTMLButtonElement>("[data-synthesize-add='one']").disabled).toBe(false);
  });

  it("keeps everything when the discard confirmation is declined", async () => {
    const storage = memoryStorage();
    const fetcher = routedFetch({
      related: () => jsonResponse(relatedPayload("balanced", ["one"])),
    });
    await start({ fetch: fetcher, storage, clipboard: null, confirm: () => false });
    addCandidate("one");
    typeInto("[data-synthesize-draft]", "words");

    query<HTMLButtonElement>("[data-synthesize-discard]").click();
    expect(statusText()).toBe("Kept the saved selection and draft.");
    expect(selectedKeys()).toEqual(["anchor", "one"]);
    expect(query<HTMLTextAreaElement>("[data-synthesize-draft]").value).toBe("words");
    expect(
      JSON.parse(String(storage.entries.get("bn.synthesize.vault-1.anchor"))).draft
    ).toBe("words");
  });

  it("warns instead of claiming a discard when the saved record cannot be deleted", async () => {
    const storage = unclearableStorage();
    const fetcher = routedFetch({
      related: () => jsonResponse(relatedPayload("balanced", ["one"])),
    });
    await start({ fetch: fetcher, storage, clipboard: null, confirm: () => true });
    addCandidate("one");
    typeInto("[data-synthesize-draft]", "words");

    query<HTMLButtonElement>("[data-synthesize-discard]").click();
    expect(storage.entries.has("bn.synthesize.vault-1.anchor")).toBe(true);
    expect(statusText()).toBe("Cleared this page, but the saved copy could not be deleted.");
    const warning = query<HTMLElement>("[data-synthesize-warning]");
    expect(warning.hidden).toBe(false);
    expect(warning.textContent).toContain("refused to delete the saved sources and draft");
    expect(query<HTMLTextAreaElement>("[data-synthesize-draft]").value).toBe("");
  });

  it("discards an untouched workbench without asking", async () => {
    const confirm = vi.fn(() => true);
    await start({ fetch: routedFetch(), storage: memoryStorage(), clipboard: null, confirm });
    query<HTMLButtonElement>("[data-synthesize-discard]").click();
    expect(confirm).not.toHaveBeenCalled();
    expect(statusText()).toBe("Discarded the saved selection and draft.");
  });
});
