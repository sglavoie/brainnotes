// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import { initSynthesize, type SynthesisDeps } from "./synthesize";
import { SYNTHESIS_STORAGE_VERSION, type SynthesisStorageLike } from "./synthesize-storage";

const jsonResponse = (body: unknown, status = 200): Response =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

const identity = (key: string): Record<string, unknown> => ({
  key,
  title: `${key} title`,
  path: `Notes/${key}.md`,
  route: `/n/${key}`,
  folder: "Notes",
});

const relatedPayload = (mode: string, keys: string[]): Record<string, unknown> => ({
  server_date: "2026-08-19",
  source: identity("anchor"),
  mode,
  has_more: false,
  results: keys.map((key) => ({
    candidate: identity(key),
    excerpt: `${key} excerpt`,
    relationship: "direct",
    reasons: [],
    useful: false,
  })),
  feedback: [],
});

const contextPayload = (candidates: string[], overrides: Record<string, unknown> = {}) => ({
  ok: true,
  context_id: "ctx-1",
  expires_at: "2026-08-19T12:00:00Z",
  vault_id: "vault-1",
  agent_enabled: false,
  purpose: "synthesis",
  prompt: "",
  anchor: identity("anchor"),
  supporters: candidates.map(identity),
  manifest: {
    source_path: "Notes/anchor.md",
    max_bytes: 40960,
    byte_count: 2048,
    notes: [identity("anchor"), ...candidates.map(identity)],
    excerpts: [{ path: "Notes/one.md", start_line: 4, end_line: 9 }],
  },
  markdown: "# Packet\n\nexact bytes\n",
  ...overrides,
});

const generatePayload = (overrides: Record<string, unknown> = {}) => ({
  ok: true,
  enabled: true,
  context_id: "ctx-1",
  output: "# Generated\n\nagent prose\n",
  rendered_html: "<h1>Generated</h1>",
  ...overrides,
});

const previewPayload = (body: URLSearchParams, overrides: Record<string, unknown> = {}) => {
  const title = body.get("title") || "";
  const folder = body.get("folder") || "";
  return {
    ok: true,
    preview_id: "prev-1",
    expires_at: "2026-08-19T12:05:00Z",
    context_id: body.get("context_id") || "ctx-1",
    destination: folder === "" ? `${title}.md` : `${folder}/${title}.md`,
    markdown: `---\ntitle: ${title}\n---\n\n${body.get("draft") || ""}\n`,
    rendered_html: "<p>rendered note</p>",
    ...overrides,
  };
};

const applyPayload = (overrides: Record<string, unknown> = {}) => ({
  ok: true,
  message: "Created note.",
  key: "Synthesis/Result",
  path: "/n/Synthesis/Result",
  rel_path: "Synthesis/Result.md",
  journal_id: "journal-1",
  ...overrides,
});

const memoryStorage = (): SynthesisStorageLike & { entries: Map<string, string> } => {
  const entries = new Map<string, string>();
  return {
    entries,
    getItem: (key) => (entries.has(key) ? String(entries.get(key)) : null),
    setItem: (key, value) => {
      entries.set(key, value);
    },
    removeItem: (key) => {
      entries.delete(key);
    },
  };
};

const unclearableStorage = (): SynthesisStorageLike & { entries: Map<string, string> } => ({
  ...memoryStorage(),
  removeItem: () => {
    throw new Error("storage is disabled");
  },
});

const mount = (agent: "enabled" | "disabled" = "enabled"): void => {
  document.body.innerHTML = `
    <section
      data-synthesize-root
      data-synthesize-csrf="csrf-token"
      data-synthesize-vault="vault-1"
      data-synthesize-anchor-key="anchor"
      data-synthesize-anchor-title="Anchor note"
      data-synthesize-anchor-path="Notes/anchor.md"
      data-synthesize-anchor-route="/n/anchor"
      data-synthesize-default-folder="Synthesis"
      data-synthesize-default-purpose="synthesis"
      data-synthesize-purposes="synthesis,contradictions,questions"
      data-synthesize-max-supporters="3"
      data-synthesize-agent="${agent}"
      data-synthesize-context-endpoint="/api/synthesis/context"
      data-synthesize-generate-endpoint="/api/synthesis/generate"
      data-synthesize-preview-endpoint="/api/synthesis/preview"
      data-synthesize-apply-endpoint="/api/synthesis/apply"
      data-synthesize-related-endpoint="/api/related"
      data-synthesize-search-endpoint="/api/search"
    >
      <p data-synthesize-status role="status"></p>
      <p data-synthesize-fallback>Needs JavaScript.</p>
      <div data-synthesize-workbench></div>
    </section>
  `;
};

// Consent and navigation are stubbed by default so a test that is not about
// them does not have to opt out of jsdom's unimplemented window methods.
const start = async (
  deps: SynthesisDeps,
  agent: "enabled" | "disabled" = "enabled"
): Promise<void> => {
  mount(agent);
  const controller = initSynthesize({ confirm: () => true, redirect: () => undefined, ...deps });
  expect(controller).not.toBeNull();
  await controller?.ready;
  await Promise.resolve();
};

const routedFetch = (
  handlers: {
    related?: (url: URL) => Response | Promise<Response>;
    search?: (url: URL) => Response | Promise<Response>;
    context?: (body: URLSearchParams) => Response | Promise<Response>;
    generate?: (body: URLSearchParams) => Response | Promise<Response>;
    preview?: (body: URLSearchParams) => Response | Promise<Response>;
    apply?: (body: URLSearchParams) => Response | Promise<Response>;
  } = {}
) =>
  vi.fn(async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const url = new URL(String(input), "https://example.test");
    const body = new URLSearchParams(String(init?.body || ""));
    if (url.pathname === "/api/related") {
      return (
        handlers.related?.(url) ||
        jsonResponse(relatedPayload(url.searchParams.get("mode") || "balanced", []))
      );
    }
    if (url.pathname === "/api/search") {
      return handlers.search?.(url) || jsonResponse([]);
    }
    if (url.pathname === "/api/synthesis/context") {
      return handlers.context?.(body) || jsonResponse(contextPayload(body.getAll("candidate")));
    }
    if (url.pathname === "/api/synthesis/generate") {
      return handlers.generate?.(body) || jsonResponse(generatePayload());
    }
    if (url.pathname === "/api/synthesis/preview") {
      return handlers.preview?.(body) || jsonResponse(previewPayload(body));
    }
    if (url.pathname === "/api/synthesis/apply") {
      return handlers.apply?.(body) || jsonResponse(applyPayload());
    }
    throw new Error(`unexpected request to ${url.pathname}`);
  });

const callsTo = (fetcher: ReturnType<typeof routedFetch>, path: string): unknown[][] =>
  fetcher.mock.calls.filter((call) => String(call[0]) === path);

const workbench = (): HTMLElement => {
  const element = document.querySelector<HTMLElement>("[data-synthesize-workbench]");
  if (!element) {
    throw new Error("workbench is missing");
  }
  return element;
};

const query = <T extends Element>(selector: string): T => {
  const element = document.querySelector<T>(selector);
  if (!element) {
    throw new Error(`missing ${selector}`);
  }
  return element;
};

const selectedKeys = (): string[] =>
  Array.from(document.querySelectorAll<HTMLElement>("[data-synthesize-source]")).map(
    (row) => row.getAttribute("data-synthesize-source") || ""
  );

const statusText = (): string => query<HTMLElement>("[data-synthesize-status]").textContent || "";

const addCandidate = (key: string): void => {
  query<HTMLButtonElement>(`[data-synthesize-add="${key}"]`).click();
};

const typeInto = (selector: string, value: string): void => {
  const field = query<HTMLInputElement | HTMLTextAreaElement>(selector);
  field.value = value;
  field.dispatchEvent(new Event("input", { bubbles: true }));
};

const prepareContext = async (): Promise<void> => {
  query<HTMLButtonElement>("[data-synthesize-prepare]").click();
  await vi.waitFor(() => {
    expect(query<HTMLElement>("[data-synthesize-packet]").textContent).not.toBe("");
  });
};

const generateCandidate = async (): Promise<void> => {
  query<HTMLButtonElement>("[data-synthesize-generate]").click();
  await vi.waitFor(() => {
    expect(query<HTMLElement>("[data-synthesize-candidate-panel]").hidden).toBe(false);
  });
};

const previewNote = async (): Promise<void> => {
  query<HTMLButtonElement>("[data-synthesize-preview]").click();
  await vi.waitFor(() => {
    expect(query<HTMLElement>("[data-synthesize-preview-panel]").hidden).toBe(false);
  });
};

const deferredResponse = (): { promise: Promise<Response>; release: (value: Response) => void } => {
  let release: (value: Response) => void = () => {};
  const promise = new Promise<Response>((resolve) => {
    release = resolve;
  });
  return { promise, release };
};

const settle = async (): Promise<void> => {
  for (let tick = 0; tick < 10; tick += 1) {
    await new Promise((resolve) => setTimeout(resolve, 0));
  }
};

const dragRow = (key: string, clientY: number): void => {
  const handle = query<HTMLButtonElement>(`[data-synthesize-handle="${key}"]`);
  handle.dispatchEvent(new MouseEvent("pointerdown", { button: 0, bubbles: true }));
  window.dispatchEvent(new MouseEvent("pointermove", { clientY, bubbles: true }));
  window.dispatchEvent(new MouseEvent("pointerup", { bubbles: true }));
};

afterEach(() => {
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

describe("synthesis workbench mounting", () => {
  it("renders the anchor first, hides the no-JavaScript fallback, and loads balanced suggestions", async () => {
    const fetcher = routedFetch({
      related: (url) =>
        jsonResponse(relatedPayload(url.searchParams.get("mode") || "balanced", ["one", "two"])),
    });
    await start({ fetch: fetcher, storage: memoryStorage(), clipboard: null });

    expect(query<HTMLElement>("[data-synthesize-fallback]").hidden).toBe(true);
    expect(selectedKeys()).toEqual(["anchor"]);
    expect(query<HTMLElement>("[data-synthesize-source='anchor']").hasAttribute("data-synthesize-anchor")).toBe(
      true
    );
    expect(document.querySelector("[data-synthesize-handle]")).toBeNull();
    expect(
      Array.from(
        query<HTMLElement>("[data-synthesize-suggestions='balanced']").querySelectorAll(
          "[data-synthesize-candidate]"
        )
      ).map((card) => card.getAttribute("data-synthesize-candidate"))
    ).toEqual(["one", "two"]);
    expect(query<HTMLElement>("[data-synthesize-suggestions-status]").textContent).toContain(
      "2 suggestions"
    );
  });

  it("never offers the anchor as a candidate", async () => {
    const fetcher = routedFetch({
      related: () => jsonResponse(relatedPayload("balanced", ["anchor", "one"])),
    });
    await start({ fetch: fetcher, storage: memoryStorage(), clipboard: null });
    expect(document.querySelector("[data-synthesize-add='anchor']")).toBeNull();
    expect(document.querySelector("[data-synthesize-add='one']")).not.toBeNull();
  });

  it("reports an empty mode and a failed mode without discarding the panel", async () => {
    const fetcher = routedFetch({
      related: (url) =>
        url.searchParams.get("mode") === "connect"
          ? new Response("boom", { status: 500 })
          : jsonResponse(relatedPayload(url.searchParams.get("mode") || "balanced", [])),
    });
    await start({ fetch: fetcher, storage: memoryStorage(), clipboard: null });
    expect(query<HTMLElement>("[data-synthesize-suggestions-status]").textContent).toBe(
      "No suggestions are available in this mode."
    );

    query<HTMLButtonElement>("[data-synthesize-tab='connect']").click();
    await vi.waitFor(() => {
      expect(query<HTMLElement>("[data-synthesize-suggestions-status]").textContent).toBe(
        "Related notes request failed (500)."
      );
    });
    expect(query<HTMLElement>("[data-synthesize-panel='connect']").hidden).toBe(false);
    expect(query<HTMLElement>("[data-synthesize-panel='balanced']").hidden).toBe(true);
  });

  it("ignores a suggestion response that a later request for the same mode superseded", async () => {
    let releaseFirst: (value: Response) => void = () => {};
    const first = new Promise<Response>((resolve) => {
      releaseFirst = resolve;
    });
    let balancedCalls = 0;
    const fetcher = routedFetch({
      related: (url) => {
        const mode = url.searchParams.get("mode") || "balanced";
        if (mode !== "balanced") {
          return jsonResponse(relatedPayload(mode, ["other"]));
        }
        balancedCalls += 1;
        return balancedCalls === 1 ? first : jsonResponse(relatedPayload("balanced", ["second"]));
      },
    });
    mount();
    initSynthesize({ fetch: fetcher, storage: null, clipboard: null });

    query<HTMLButtonElement>("[data-synthesize-tab='rediscover']").click();
    await vi.waitFor(() => {
      expect(document.querySelector("[data-synthesize-add='other']")).not.toBeNull();
    });
    query<HTMLButtonElement>("[data-synthesize-tab='balanced']").click();
    await vi.waitFor(() => {
      expect(document.querySelector("[data-synthesize-add='second']")).not.toBeNull();
    });

    expect(balancedCalls).toBe(2);
    releaseFirst(jsonResponse(relatedPayload("balanced", ["first"])));
    for (let flush = 0; flush < 10; flush += 1) {
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    expect(document.querySelector("[data-synthesize-add='first']")).toBeNull();
    expect(document.querySelector("[data-synthesize-add='second']")).not.toBeNull();
  });
});

describe("synthesis source curation", () => {
  const withSuggestions = async (): Promise<ReturnType<typeof vi.fn>> => {
    const fetcher = routedFetch({
      related: (url) =>
        jsonResponse(
          relatedPayload(url.searchParams.get("mode") || "balanced", ["one", "two", "three", "four"])
        ),
    });
    await start({ fetch: fetcher, storage: memoryStorage(), clipboard: null });
    return fetcher;
  };

  it("adds, refuses a duplicate, and stops at the configured maximum", async () => {
    await withSuggestions();
    addCandidate("one");
    expect(selectedKeys()).toEqual(["anchor", "one"]);
    expect(query<HTMLButtonElement>("[data-synthesize-add='one']").disabled).toBe(true);
    expect(query<HTMLButtonElement>("[data-synthesize-add='one']").textContent).toBe("Selected");

    addCandidate("two");
    addCandidate("three");
    addCandidate("four");
    expect(selectedKeys()).toEqual(["anchor", "one", "two", "three"]);
    expect(statusText()).toBe("Only 3 supporting notes can be selected. Remove one first.");
    expect(query<HTMLElement>("[data-synthesize-selection-status]").textContent).toContain(
      "Remove one to add another."
    );
  });

  it("moves a source with the keyboard, announces the position, and keeps focus on the handle", async () => {
    await withSuggestions();
    addCandidate("one");
    addCandidate("two");

    const handle = query<HTMLButtonElement>("[data-synthesize-handle='two']");
    handle.focus();
    handle.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowUp", bubbles: true }));

    expect(selectedKeys()).toEqual(["anchor", "two", "one"]);
    expect(statusText()).toBe("Moved two title to position 2 of 3.");
    expect(document.activeElement).toBe(
      query<HTMLButtonElement>("[data-synthesize-handle='two']")
    );
  });

  it("refuses to move the first supporter above the anchor", async () => {
    await withSuggestions();
    addCandidate("one");
    const handle = query<HTMLButtonElement>("[data-synthesize-handle='one']");
    handle.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowUp", bubbles: true }));
    expect(selectedKeys()).toEqual(["anchor", "one"]);
    expect(statusText()).toBe("one title is already at the edge of the order.");
  });

  it("commits a drag to the model without ever passing the anchor", async () => {
    await withSuggestions();
    addCandidate("one");
    addCandidate("two");
    addCandidate("three");
    for (const row of document.querySelectorAll<HTMLElement>("[data-synthesize-source]")) {
      const index = selectedKeys().indexOf(row.getAttribute("data-synthesize-source") || "");
      row.getBoundingClientRect = (): DOMRect =>
        ({ top: index * 20, height: 20 }) as DOMRect;
    }

    dragRow("three", 5);
    expect(selectedKeys()).toEqual(["anchor", "three", "one", "two"]);
    expect(statusText()).toBe("Moved three title to position 2 of 4.");
    expect(document.activeElement).toBe(
      query<HTMLButtonElement>("[data-synthesize-handle='three']")
    );
  });

  it("removes a source and moves focus to the row that took its place", async () => {
    await withSuggestions();
    addCandidate("one");
    addCandidate("two");
    query<HTMLButtonElement>("[data-synthesize-remove='one']").click();

    expect(selectedKeys()).toEqual(["anchor", "two"]);
    expect(statusText()).toBe("Removed one title.");
    expect(document.activeElement).toBe(
      query<HTMLButtonElement>("[data-synthesize-remove='two']")
    );
    expect(query<HTMLButtonElement>("[data-synthesize-add='one']").disabled).toBe(false);
  });
});

describe("synthesis vault search", () => {
  it("adds a searched note using its vault key rather than its route", async () => {
    const fetcher = routedFetch({
      search: (url) =>
        url.searchParams.get("q") === "topic"
          ? jsonResponse([
              { key: "Folder/found", path: "/n/Folder/found", title: "Found", snippet: "hit" },
            ])
          : jsonResponse([]),
    });
    await start({ fetch: fetcher, storage: memoryStorage(), clipboard: null });

    query<HTMLInputElement>("[data-synthesize-search-input]").value = "topic";
    query<HTMLFormElement>("[data-synthesize-search-form]").dispatchEvent(
      new Event("submit", { bubbles: true, cancelable: true })
    );
    await vi.waitFor(() => {
      expect(document.querySelector("[data-synthesize-add='Folder/found']")).not.toBeNull();
    });
    expect(query<HTMLElement>("[data-synthesize-search-status]").textContent).toBe(
      "1 match for topic."
    );

    addCandidate("Folder/found");
    expect(selectedKeys()).toEqual(["anchor", "Folder/found"]);
    expect(
      query<HTMLElement>("[data-synthesize-source='Folder/found'] .synthesize-source-path")
        .textContent
    ).toBe("Folder/found");
  });

  it("reports an empty search and a failed search", async () => {
    const fetcher = routedFetch({
      search: (url) =>
        url.searchParams.get("q") === "boom"
          ? new Response("search error", { status: 500 })
          : jsonResponse([]),
    });
    await start({ fetch: fetcher, storage: memoryStorage(), clipboard: null });
    const form = query<HTMLFormElement>("[data-synthesize-search-form]");
    const input = query<HTMLInputElement>("[data-synthesize-search-input]");

    input.value = "nothing";
    form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await vi.waitFor(() => {
      expect(query<HTMLElement>("[data-synthesize-search-status]").textContent).toBe(
        "No notes matched that search."
      );
    });

    input.value = "boom";
    form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await vi.waitFor(() => {
      expect(query<HTMLElement>("[data-synthesize-search-status]").textContent).toBe(
        "search error"
      );
    });
  });
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

describe("synthesis generation", () => {
  const withContext = async (
    handlers: Parameters<typeof routedFetch>[0] = {},
    deps: SynthesisDeps = {},
    agent: "enabled" | "disabled" = "enabled"
  ): Promise<ReturnType<typeof routedFetch>> => {
    const fetcher = routedFetch({
      related: () => jsonResponse(relatedPayload("balanced", ["one"])),
      ...handlers,
    });
    await start({ fetch: fetcher, storage: memoryStorage(), clipboard: null, ...deps }, agent);
    addCandidate("one");
    await prepareContext();
    typeInto("[data-synthesize-draft]", "my own words");
    return fetcher;
  };

  it("asks for consent once and never puts the candidate into the draft", async () => {
    let generations = 0;
    const confirm = vi.fn((message: string) => message !== "");
    const fetcher = await withContext(
      {
        generate: () => {
          generations += 1;
          return jsonResponse(
            generatePayload({
              output: `candidate ${generations}`,
              rendered_html: `<p>candidate ${generations}</p>`,
            })
          );
        },
      },
      { confirm }
    );

    await generateCandidate();
    expect(confirm).toHaveBeenCalledTimes(1);
    expect(String(confirm.mock.calls[0][0])).toContain("[agent].command");
    expect(query<HTMLElement>("[data-synthesize-candidate-markdown]").textContent).toBe(
      "candidate 1"
    );
    expect(query<HTMLElement>("[data-synthesize-candidate-rendered]").innerHTML).toBe(
      "<p>candidate 1</p>"
    );
    expect(query<HTMLTextAreaElement>("[data-synthesize-draft]").value).toBe("my own words");

    query<HTMLButtonElement>("[data-synthesize-generate]").click();
    await vi.waitFor(() => {
      expect(query<HTMLElement>("[data-synthesize-candidate-markdown]").textContent).toBe(
        "candidate 2"
      );
    });
    expect(confirm).toHaveBeenCalledTimes(1);
    expect(query<HTMLTextAreaElement>("[data-synthesize-draft]").value).toBe("my own words");
    expect(callsTo(fetcher, "/api/synthesis/generate")).toHaveLength(2);
    expect(
      new URLSearchParams(
        String((callsTo(fetcher, "/api/synthesis/generate")[0][1] as RequestInit).body)
      ).get("context_id")
    ).toBe("ctx-1");
  });

  it("states the server's real retention before asking for consent", async () => {
    const confirm = vi.fn((_message: string) => false);
    const fetcher = await withContext({}, { confirm });
    query<HTMLButtonElement>("[data-synthesize-generate]").click();
    expect(confirm).toHaveBeenCalledTimes(1);
    const message = String(confirm.mock.calls[0][0]);
    expect(message).toContain(
      "Your prompt and that packet are retained in server memory until the context expires, is evicted to keep memory bounded, or the server restarts, and are never written to disk."
    );
    expect(message).toContain("The stored packet is sent to the command only after you accept this notice.");
    expect(message).toContain("The generated candidate is not stored on the server");
    expect(callsTo(fetcher, "/api/synthesis/generate")).toHaveLength(0);
  });

  it("sends nothing when the disclosure is declined", async () => {
    const fetcher = await withContext({}, { confirm: () => false });
    query<HTMLButtonElement>("[data-synthesize-generate]").click();
    expect(query<HTMLElement>("[data-synthesize-generate-status]").textContent).toBe(
      "Generation canceled."
    );
    expect(callsTo(fetcher, "/api/synthesis/generate")).toHaveLength(0);
    expect(query<HTMLElement>("[data-synthesize-candidate-panel]").hidden).toBe(true);
  });

  it("reports the disabled agent without asking for consent or losing the draft", async () => {
    const confirm = vi.fn(() => true);
    await withContext(
      {
        generate: () =>
          jsonResponse({
            ok: true,
            enabled: false,
            context_id: "ctx-1",
            message: "Generation is unavailable until [agent].command is configured.",
          }),
      },
      { confirm },
      "disabled"
    );

    query<HTMLButtonElement>("[data-synthesize-generate]").click();
    await vi.waitFor(() => {
      expect(query<HTMLElement>("[data-synthesize-generate-status]").textContent).toBe(
        "Generation is unavailable until [agent].command is configured."
      );
    });
    expect(confirm).not.toHaveBeenCalled();
    expect(query<HTMLElement>("[data-synthesize-candidate-panel]").hidden).toBe(true);
    expect(query<HTMLTextAreaElement>("[data-synthesize-draft]").value).toBe("my own words");
  });

  it("replaces, appends, and dismisses the candidate only when asked", async () => {
    await withContext();
    await generateCandidate();

    query<HTMLButtonElement>("[data-synthesize-candidate-append]").click();
    expect(query<HTMLTextAreaElement>("[data-synthesize-draft]").value).toBe(
      "my own words\n\n# Generated\n\nagent prose\n"
    );
    expect(query<HTMLElement>("[data-synthesize-generate-status]").textContent).toBe(
      "Candidate appended to the draft."
    );

    query<HTMLButtonElement>("[data-synthesize-candidate-replace]").click();
    expect(query<HTMLTextAreaElement>("[data-synthesize-draft]").value).toBe(
      "# Generated\n\nagent prose\n"
    );

    query<HTMLButtonElement>("[data-synthesize-candidate-dismiss]").click();
    expect(query<HTMLElement>("[data-synthesize-candidate-panel]").hidden).toBe(true);
    expect(query<HTMLTextAreaElement>("[data-synthesize-draft]").value).toBe(
      "# Generated\n\nagent prose\n"
    );
  });

  it("copies the candidate and disables copying with no writable clipboard", async () => {
    const writeText = vi.fn<(text: string) => Promise<void>>().mockResolvedValue(undefined);
    await withContext({}, { clipboard: { writeText } });
    await generateCandidate();
    query<HTMLButtonElement>("[data-synthesize-candidate-copy]").click();
    await vi.waitFor(() => {
      expect(query<HTMLElement>("[data-synthesize-generate-status]").textContent).toBe(
        "Candidate copied."
      );
    });
    expect(writeText).toHaveBeenCalledWith("# Generated\n\nagent prose\n");

    await withContext();
    await generateCandidate();
    expect(query<HTMLButtonElement>("[data-synthesize-candidate-copy]").disabled).toBe(true);
  });

  it("keeps the draft when the generation command fails", async () => {
    await withContext({
      generate: () =>
        jsonResponse(
          { ok: false, code: "internal_error", message: "synthesis: generation command timed out" },
          500
        ),
    });
    query<HTMLButtonElement>("[data-synthesize-generate]").click();
    await vi.waitFor(() => {
      expect(query<HTMLElement>("[data-synthesize-generate-status]").textContent).toBe(
        "synthesis: generation command timed out"
      );
    });
    expect(query<HTMLElement>("[data-synthesize-candidate-panel]").hidden).toBe(true);
    expect(query<HTMLTextAreaElement>("[data-synthesize-draft]").value).toBe("my own words");
    expect(query<HTMLButtonElement>("[data-synthesize-generate]").disabled).toBe(false);
  });

  it("drops an expired context without touching the text the user wrote", async () => {
    await withContext({
      generate: () =>
        jsonResponse({ ok: false, code: "not_found", message: "context expired" }, 404),
    });
    query<HTMLButtonElement>("[data-synthesize-generate]").click();
    await vi.waitFor(() => {
      expect(query<HTMLElement>("[data-synthesize-generate-status]").textContent).toContain(
        "Rebuild it, then preview again."
      );
    });
    expect(query<HTMLElement>("[data-synthesize-packet]").textContent).toBe("");
    expect(query<HTMLButtonElement>("[data-synthesize-prepare]").textContent).toBe(
      "Rebuild context"
    );
    expect(query<HTMLButtonElement>("[data-synthesize-generate]").disabled).toBe(true);
    expect(query<HTMLTextAreaElement>("[data-synthesize-draft]").value).toBe("my own words");
  });
});

describe("synthesis creation", () => {
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
    expect(storage.entries.has("bn.synthesize.vault-1.anchor")).toBe(false);
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
    expect(storage.entries.has("bn.synthesize.vault-1.anchor")).toBe(true);
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
      JSON.parse(String(storage.entries.get("bn.synthesize.vault-1.anchor"))).draft
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

describe("synthesis DOM contract", () => {
  it("exposes one column stack and stable hooks for every control", async () => {
    await start({ fetch: routedFetch(), storage: memoryStorage(), clipboard: null });
    for (const selector of [
      "[data-synthesize-source-list]",
      "[data-synthesize-selection-status]",
      "[data-synthesize-suggestions='balanced']",
      "[data-synthesize-suggestions='rediscover']",
      "[data-synthesize-suggestions='connect']",
      "[data-synthesize-suggestions-status]",
      "[data-synthesize-search-form]",
      "[data-synthesize-search-input]",
      "[data-synthesize-search-results]",
      "[data-synthesize-search-status]",
      "[data-synthesize-title]",
      "[data-synthesize-title-error]",
      "[data-synthesize-folder]",
      "[data-synthesize-folder-error]",
      "[data-synthesize-purpose]",
      "[data-synthesize-prompt]",
      "[data-synthesize-draft]",
      "[data-synthesize-draft-error]",
      "[data-synthesize-prepare]",
      "[data-synthesize-copy]",
      "[data-synthesize-discard]",
      "[data-synthesize-context-status]",
      "[data-synthesize-manifest]",
      "[data-synthesize-packet]",
      "[data-synthesize-generate]",
      "[data-synthesize-generate-status]",
      "[data-synthesize-candidate-panel]",
      "[data-synthesize-candidate-rendered]",
      "[data-synthesize-candidate-markdown]",
      "[data-synthesize-candidate-replace]",
      "[data-synthesize-candidate-append]",
      "[data-synthesize-candidate-copy]",
      "[data-synthesize-candidate-dismiss]",
      "[data-synthesize-destination]",
      "[data-synthesize-preview]",
      "[data-synthesize-apply]",
      "[data-synthesize-preview-status]",
      "[data-synthesize-preview-panel]",
      "[data-synthesize-preview-destination]",
      "[data-synthesize-preview-expiry]",
      "[data-synthesize-preview-rendered]",
      "[data-synthesize-preview-markdown]",
      "[data-synthesize-preview-sources]",
      "[data-synthesize-warning]",
    ]) {
      expect(document.querySelectorAll(selector)).toHaveLength(1);
    }
    for (const selector of [
      "[data-synthesize-title-error]",
      "[data-synthesize-folder-error]",
      "[data-synthesize-draft-error]",
      "[data-synthesize-candidate-panel]",
      "[data-synthesize-preview-panel]",
    ]) {
      expect(query<HTMLElement>(selector).hidden).toBe(true);
    }
    expect(query<HTMLInputElement>("[data-synthesize-title]").getAttribute("aria-describedby")).toBe(
      "synthesize-title-error"
    );
    expect(query<HTMLButtonElement>("[data-synthesize-generate]").disabled).toBe(true);
    expect(query<HTMLButtonElement>("[data-synthesize-apply]").disabled).toBe(true);
    for (const selector of [
      "[data-synthesize-generate-status]",
      "[data-synthesize-preview-status]",
    ]) {
      expect(query<HTMLElement>(selector).getAttribute("aria-live")).toBe("polite");
    }
    expect(workbench().querySelectorAll(".synthesize-column")).toHaveLength(2);
    expect(query<HTMLElement>("[data-synthesize-warning]").hidden).toBe(true);
    expect(
      query<HTMLElement>("[data-synthesize-panel='balanced']").getAttribute("role")
    ).toBe("tabpanel");
    expect(
      Array.from(document.querySelectorAll<HTMLSelectElement>("[data-synthesize-purpose] option")).map(
        (option) => option.value
      )
    ).toEqual(["synthesis", "contradictions", "questions"]);
  });

  it("does nothing on a page with no synthesis root", () => {
    document.body.innerHTML = "<main></main>";
    expect(initSynthesize({ fetch: routedFetch(), storage: null, clipboard: null })).toBeNull();
  });
});
