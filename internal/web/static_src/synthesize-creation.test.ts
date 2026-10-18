// @vitest-environment jsdom

import { afterEach, expect, it, vi } from "vitest";

import type { SynthesisDeps } from "./synthesize";
import {
  addCandidate,
  applyPayload,
  callsTo,
  jsonResponse,
  memoryStorage,
  prepareContext,
  previewNote,
  query,
  relatedPayload,
  routedFetch,
  start,
  typeInto,
  unclearableStorage,
} from "./synthesize.test-helpers";

afterEach(() => {
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

  const ready = async (
    handlers: Parameters<typeof routedFetch>[0] = {},
    deps: SynthesisDeps = {}
  ): Promise<ReturnType<typeof routedFetch>> => {
    const fetcher = routedFetch({
      related: () => jsonResponse(relatedPayload("balanced", ["one"])),
      ...handlers,
    });
    await start({ fetch: fetcher, storage: memoryStorage(), clipboard: null, ...deps });
    addCandidate("one");
    await prepareContext();
    typeInto("[data-synthesize-title]", "Result");
    typeInto("[data-synthesize-draft]", "what these add up to");
    return fetcher;
  };

  it("shows the destination as soon as a title exists", async () => {
    await start({ fetch: routedFetch(), storage: memoryStorage(), clipboard: null });
    expect(query<HTMLElement>("[data-synthesize-destination]").textContent).toBe(
      "Enter a title to choose where the note is created."
    );
    typeInto("[data-synthesize-title]", "Result");
    expect(query<HTMLElement>("[data-synthesize-destination]").textContent).toBe(
      "Creates Synthesis/Result.md"
    );
  });

  it("refuses to preview an invalid field and says which one beside it", async () => {
    const fetcher = await ready();
    typeInto("[data-synthesize-title]", "");
    query<HTMLButtonElement>("[data-synthesize-preview]").click();
    expect(query<HTMLElement>("[data-synthesize-title-error]").hidden).toBe(false);
    expect(query<HTMLElement>("[data-synthesize-title-error]").textContent).toBe(
      "A note title is required."
    );
    expect(document.activeElement).toBe(query<HTMLInputElement>("[data-synthesize-title]"));
    expect(callsTo(fetcher, "/api/synthesis/preview")).toHaveLength(0);

    typeInto("[data-synthesize-title]", "Result");
    typeInto("[data-synthesize-draft]", "words\n\n## Sources\n\n- one\n");
    query<HTMLButtonElement>("[data-synthesize-preview]").click();
    expect(query<HTMLElement>("[data-synthesize-title-error]").hidden).toBe(true);
    expect(query<HTMLElement>("[data-synthesize-draft-error]").textContent).toContain(
      "may not contain a '## Sources' heading"
    );
    expect(callsTo(fetcher, "/api/synthesis/preview")).toHaveLength(0);
  });

  it("previews the destination, the exact Markdown, the expiry, and the sources", async () => {
    const fetcher = await ready();
    await previewNote();

    const body = new URLSearchParams(
      String((callsTo(fetcher, "/api/synthesis/preview")[0][1] as RequestInit).body)
    );
    expect(body.get("context_id")).toBe("ctx-1");
    expect(body.get("title")).toBe("Result");
    expect(body.get("folder")).toBe("Synthesis");
    expect(body.get("draft")).toBe("what these add up to");
    expect(body.get("token")).toBe("csrf-token");

    expect(query<HTMLElement>("[data-synthesize-preview-destination]").textContent).toBe(
      "Creates Synthesis/Result.md"
    );
    expect(query<HTMLElement>("[data-synthesize-preview-expiry]").textContent).toContain(
      "This preview can be applied until"
    );
    expect(query<HTMLElement>("[data-synthesize-preview-markdown]").textContent).toBe(
      "---\ntitle: Result\n---\n\nwhat these add up to\n"
    );
    expect(query<HTMLElement>("[data-synthesize-preview-rendered]").innerHTML).toBe(
      "<p>rendered note</p>"
    );
    expect(
      Array.from(
        query<HTMLElement>("[data-synthesize-preview-sources]").querySelectorAll("code")
      ).map((code) => code.textContent)
    ).toEqual(["Notes/anchor.md", "Notes/one.md"]);
    expect(query<HTMLButtonElement>("[data-synthesize-apply]").disabled).toBe(false);
  });

  it("invalidates the receipt when the note bytes or the packet would change", async () => {
    await ready();
    for (const change of [
      (): void => typeInto("[data-synthesize-title]", "Another title"),
      (): void => typeInto("[data-synthesize-folder]", "Elsewhere"),
      (): void => typeInto("[data-synthesize-draft]", "different words"),
    ]) {
      await previewNote();
      change();
      expect(query<HTMLElement>("[data-synthesize-preview-panel]").hidden).toBe(true);
      expect(query<HTMLButtonElement>("[data-synthesize-apply]").disabled).toBe(true);
    }

    await previewNote();
    query<HTMLButtonElement>("[data-synthesize-remove='one']").click();
    expect(query<HTMLElement>("[data-synthesize-preview-panel]").hidden).toBe(true);
    expect(query<HTMLElement>("[data-synthesize-packet]").textContent).toBe("");
  });

  it("creates the note, clears saved work, and opens the note route", async () => {
    const storage = memoryStorage();
    const redirect = vi.fn((path: string) => path);
    const fetcher = routedFetch({
      related: () => jsonResponse(relatedPayload("balanced", ["one"])),
    });
    await start({ fetch: fetcher, storage, clipboard: null, redirect });
    addCandidate("one");
    await prepareContext();
    typeInto("[data-synthesize-title]", "Result");
    typeInto("[data-synthesize-draft]", "what these add up to");
    await previewNote();

    query<HTMLButtonElement>("[data-synthesize-apply]").click();
    await vi.waitFor(() => {
      expect(redirect).toHaveBeenCalledWith("/n/Synthesis/Result");
    });
    const body = new URLSearchParams(
      String((callsTo(fetcher, "/api/synthesis/apply")[0][1] as RequestInit).body)
    );
    expect(body.get("preview_id")).toBe("prev-1");
    expect(body.get("token")).toBe("csrf-token");
    expect(storage.entries.has("bn.synthesis-draft.[\"vault-1\",\"anchor\"].test")).toBe(false);
    expect(query<HTMLElement>("[data-synthesize-preview-status]").textContent).toContain(
      "Created Synthesis/Result.md"
    );
  });

  it("holds the redirect and warns when the saved work cannot be deleted", async () => {
    const storage = unclearableStorage();
    const redirect = vi.fn((path: string) => path);
    const fetcher = routedFetch({
      related: () => jsonResponse(relatedPayload("balanced", ["one"])),
    });
    await start({ fetch: fetcher, storage, clipboard: null, redirect });
    addCandidate("one");
    await prepareContext();
    typeInto("[data-synthesize-title]", "Result");
    typeInto("[data-synthesize-draft]", "what these add up to");
    await previewNote();

    query<HTMLButtonElement>("[data-synthesize-apply]").click();
    await vi.waitFor(() => {
      expect(query<HTMLElement>("[data-synthesize-preview-status]").textContent).toContain(
        "Created Synthesis/Result.md"
      );
    });
    expect(redirect).not.toHaveBeenCalled();
    expect(query<HTMLElement>("[data-synthesize-preview-status]").textContent).toContain(
      "could not be deleted"
    );
    const warning = query<HTMLElement>("[data-synthesize-warning]");
    expect(warning.hidden).toBe(false);
    expect(warning.textContent).toContain("refused to delete the saved sources and draft");
    const link = query<HTMLAnchorElement>("[data-synthesize-warning-link]");
    expect(link.getAttribute("href")).toBe("/n/Synthesis/Result");
    expect(link.textContent).toBe("Open Synthesis/Result.md");
    expect(query<HTMLButtonElement>("[data-synthesize-apply]").disabled).toBe(true);
    expect(storage.entries.has("bn.synthesis-draft.[\"vault-1\",\"anchor\"].test")).toBe(true);
  });

  it("keeps the draft and spends the receipt when the create is refused", async () => {
    const storage = memoryStorage();
    const fetcher = routedFetch({
      related: () => jsonResponse(relatedPayload("balanced", ["one"])),
      apply: () =>
        jsonResponse(
          { ok: false, code: "conflict", message: "synthesis: Notes/one.md changed" },
          409
        ),
    });
    await start({ fetch: fetcher, storage, clipboard: null });
    addCandidate("one");
    await prepareContext();
    typeInto("[data-synthesize-title]", "Result");
    typeInto("[data-synthesize-draft]", "what these add up to");
    await previewNote();

    query<HTMLButtonElement>("[data-synthesize-apply]").click();
    await vi.waitFor(() => {
      expect(query<HTMLElement>("[data-synthesize-preview-status]").textContent).toContain(
        "nothing you wrote was lost"
      );
    });
    expect(query<HTMLElement>("[data-synthesize-preview-status]").textContent).toContain(
      "synthesis: Notes/one.md changed"
    );
    expect(query<HTMLButtonElement>("[data-synthesize-apply]").disabled).toBe(true);
    expect(query<HTMLElement>("[data-synthesize-preview-panel]").hidden).toBe(true);
    expect(query<HTMLTextAreaElement>("[data-synthesize-draft]").value).toBe(
      "what these add up to"
    );
    expect(
      JSON.parse(String(storage.entries.get("bn.synthesis-draft.[\"vault-1\",\"anchor\"].test"))).draft
    ).toBe("what these add up to");
  });

  it("sends one create request however often the button is pressed", async () => {
    let release: (value: Response) => void = () => {};
    const pending = new Promise<Response>((resolve) => {
      release = resolve;
    });
    const redirect = vi.fn((path: string) => path);
    const fetcher = routedFetch({
      related: () => jsonResponse(relatedPayload("balanced", ["one"])),
      apply: () => pending,
    });
    await start({ fetch: fetcher, storage: memoryStorage(), clipboard: null, redirect });
    addCandidate("one");
    await prepareContext();
    typeInto("[data-synthesize-title]", "Result");
    typeInto("[data-synthesize-draft]", "what these add up to");
    await previewNote();

    const applyButton = query<HTMLButtonElement>("[data-synthesize-apply]");
    applyButton.click();
    expect(applyButton.disabled).toBe(true);
    applyButton.click();
    applyButton.click();
    release(jsonResponse(applyPayload()));
    await vi.waitFor(() => {
      expect(redirect).toHaveBeenCalledWith("/n/Synthesis/Result");
    });
    expect(callsTo(fetcher, "/api/synthesis/apply")).toHaveLength(1);
  });

  it("directs the user to rebuild when the context expired before the preview", async () => {
    await ready({
      preview: () =>
        jsonResponse({ ok: false, code: "not_found", message: "context expired" }, 404),
    });
    query<HTMLButtonElement>("[data-synthesize-preview]").click();
    await vi.waitFor(() => {
      expect(query<HTMLElement>("[data-synthesize-preview-status]").textContent).toContain(
        "Rebuild it, then preview again."
      );
    });
    expect(query<HTMLElement>("[data-synthesize-packet]").textContent).toBe("");
    expect(query<HTMLTextAreaElement>("[data-synthesize-draft]").value).toBe(
      "what these add up to"
    );
  });

  it("reports a stale receipt without discarding the draft", async () => {
    await ready({
      preview: () =>
        jsonResponse(
          { ok: false, code: "preview_stale", message: "synthesis: brainnotes.toml changed" },
          409
        ),
    });
    query<HTMLButtonElement>("[data-synthesize-preview]").click();
    await vi.waitFor(() => {
      expect(query<HTMLElement>("[data-synthesize-preview-status]").textContent).toBe(
        "synthesis: brainnotes.toml changed"
      );
    });
    expect(query<HTMLElement>("[data-synthesize-preview-panel]").hidden).toBe(true);
    expect(query<HTMLTextAreaElement>("[data-synthesize-draft]").value).toBe(
      "what these add up to"
    );
  });

  it("creates a note with neither storage nor a clipboard available", async () => {
    const redirect = vi.fn((path: string) => path);
    const fetcher = routedFetch({
      related: () => jsonResponse(relatedPayload("balanced", ["one"])),
    });
    await start({ fetch: fetcher, storage: null, clipboard: null, redirect });
    addCandidate("one");
    await prepareContext();
    typeInto("[data-synthesize-title]", "Result");
    typeInto("[data-synthesize-draft]", "what these add up to");
    await previewNote();
    query<HTMLButtonElement>("[data-synthesize-apply]").click();
    await vi.waitFor(() => {
      expect(redirect).toHaveBeenCalledWith("/n/Synthesis/Result");
    });
    expect(query<HTMLElement>("[data-synthesize-warning]").hidden).toBe(true);
  });
