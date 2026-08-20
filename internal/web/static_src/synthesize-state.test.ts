import { describe, expect, it } from "vitest";

import {
  createSynthesisModel,
  invalidateSynthesisContext,
  synthesisSelectedKeys,
  synthesisSourcePosition,
  type SynthesisConfig,
  type SynthesisModel,
} from "./synthesize-model";
import {
  addSynthesisSource,
  candidateFromSearchResult,
  dedupeSynthesisSources,
  moveSynthesisSource,
  removeSynthesisSource,
  reorderSynthesisSources,
  type SynthesisCandidate,
} from "./synthesize-sources";
import {
  SYNTHESIS_STORAGE_VERSION,
  clearSynthesisState,
  readSynthesisState,
  safeSynthesisStorage,
  synthesisStateFromModel,
  synthesisStorageKey,
  writeSynthesisState,
  type SynthesisStorageLike,
} from "./synthesize-storage";
import type { SynthesisContextResponse } from "./api";

const config = (overrides: Partial<SynthesisConfig> = {}): SynthesisConfig => ({
  csrfToken: "csrf-token",
  vaultID: "vault-1",
  anchor: { key: "anchor", title: "Anchor", path: "Notes/anchor.md", route: "/n/anchor" },
  defaultFolder: "Synthesis",
  defaultPurpose: "synthesis",
  purposes: ["synthesis", "contradictions", "questions"],
  maxSupporters: 10,
  agentEnabled: true,
  contextEndpoint: "/api/synthesis/context",
  generateEndpoint: "/api/synthesis/generate",
  previewEndpoint: "/api/synthesis/preview",
  applyEndpoint: "/api/synthesis/apply",
  relatedEndpoint: "/api/related",
  searchEndpoint: "/api/search",
  ...overrides,
});

const candidate = (key: string): SynthesisCandidate => ({
  key,
  title: `${key} title`,
  path: `Notes/${key}.md`,
  route: `/n/${key}`,
  detail: `${key} detail`,
});

const withContext = (model: SynthesisModel): SynthesisModel => {
  model.context = { context_id: "ctx-1" } as SynthesisContextResponse;
  model.contextStale = false;
  return model;
};

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

describe("synthesis selection", () => {
  it("refuses the anchor, duplicates, and a selection past the maximum", () => {
    const cfg = config({ maxSupporters: 2 });
    const model = createSynthesisModel(cfg);
    expect(addSynthesisSource(model, cfg, candidate("anchor")).ok).toBe(false);
    expect(addSynthesisSource(model, cfg, candidate("one")).ok).toBe(true);
    expect(addSynthesisSource(model, cfg, candidate("one"))).toEqual({
      ok: false,
      message: "one title is already selected.",
    });
    expect(addSynthesisSource(model, cfg, candidate("two")).ok).toBe(true);
    expect(addSynthesisSource(model, cfg, candidate("three"))).toEqual({
      ok: false,
      message: "Only 2 supporting notes can be selected. Remove one first.",
    });
    expect(synthesisSelectedKeys(model)).toEqual(["one", "two"]);
  });

  it("numbers supporters after the anchor and announces every move", () => {
    const cfg = config();
    const model = createSynthesisModel(cfg);
    for (const key of ["one", "two", "three"]) {
      addSynthesisSource(model, cfg, candidate(key));
    }
    expect(synthesisSourcePosition(model, "one")).toBe(2);
    expect(moveSynthesisSource(model, "three", -1)).toEqual({
      ok: true,
      message: "Moved three title to position 3 of 4.",
    });
    expect(synthesisSelectedKeys(model)).toEqual(["one", "three", "two"]);
    expect(moveSynthesisSource(model, "one", -1)).toEqual({
      ok: false,
      message: "one title is already at the edge of the order.",
    });
    expect(moveSynthesisSource(model, "missing", 1).ok).toBe(false);
  });

  it("removes a selected note and reports an unknown one", () => {
    const cfg = config();
    const model = createSynthesisModel(cfg);
    addSynthesisSource(model, cfg, candidate("one"));
    expect(removeSynthesisSource(model, "one")).toEqual({ ok: true, message: "Removed one title." });
    expect(removeSynthesisSource(model, "one").ok).toBe(false);
  });

  it("keeps every selected note when a drag reports a partial or stale order", () => {
    const cfg = config();
    const model = createSynthesisModel(cfg);
    for (const key of ["one", "two", "three"]) {
      addSynthesisSource(model, cfg, candidate(key));
    }
    expect(reorderSynthesisSources(model, ["three", "gone"]).ok).toBe(true);
    expect(synthesisSelectedKeys(model)).toEqual(["three", "one", "two"]);
    expect(reorderSynthesisSources(model, ["three", "one", "two"])).toEqual({
      ok: false,
      message: "",
    });
  });

  it("drops the anchor, blanks, and duplicates when restoring a selection", () => {
    const restored = dedupeSynthesisSources(
      [
        { key: "one", title: "One", path: "Notes/one.md", route: "/n/one" },
        { key: "anchor", title: "Anchor", path: "Notes/anchor.md", route: "/n/anchor" },
        { key: "one", title: "One", path: "Notes/one.md", route: "/n/one" },
        { key: "", title: "", path: "", route: "" },
        { key: "two", title: "Two", path: "Notes/two.md", route: "/n/two" },
        { key: "three", title: "Three", path: "Notes/three.md", route: "/n/three" },
      ],
      "anchor",
      2
    );
    expect(restored.map((source) => source.key)).toEqual(["one", "two"]);
  });

  it("takes the vault path from a search result key rather than its route", () => {
    const result = candidateFromSearchResult({
      key: "Folder/note",
      path: "/n/Folder/note",
      title: "Note",
      folder: "Folder",
      matched_fields: [],
      issues: [],
      snippet: "<mark>note</mark>",
      pinned: false,
    });
    expect(result).toEqual({
      key: "Folder/note",
      title: "Note",
      path: "Folder/note",
      route: "/n/Folder/note",
      detail: "<mark>note</mark>",
    });
    expect(
      candidateFromSearchResult({
        key: "  ",
        path: "",
        title: "",
        folder: "",
        matched_fields: [],
        issues: [],
        snippet: "",
        pinned: false,
      })
    ).toBeNull();
  });
});

describe("synthesis context invalidation", () => {
  it("clears a prepared packet whenever the selection or its order changes", () => {
    const cfg = config();
    const model = createSynthesisModel(cfg);
    addSynthesisSource(model, cfg, candidate("one"));
    addSynthesisSource(model, cfg, candidate("two"));

    withContext(model);
    addSynthesisSource(model, cfg, candidate("three"));
    expect(model.context).toBeNull();
    expect(model.contextStale).toBe(true);

    withContext(model);
    moveSynthesisSource(model, "three", -1);
    expect(model.context).toBeNull();

    withContext(model);
    removeSynthesisSource(model, "three");
    expect(model.context).toBeNull();

    withContext(model);
    reorderSynthesisSources(model, ["two", "one"]);
    expect(model.context).toBeNull();
  });

  it("leaves contextStale alone when no packet was ever prepared", () => {
    const model = createSynthesisModel(config());
    invalidateSynthesisContext(model);
    expect(model.contextStale).toBe(false);
  });
});

describe("synthesis storage", () => {
  it("namespaces state by vault and anchor and stores note keys only", () => {
    const storage = memoryStorage();
    const key = synthesisStorageKey("vault-1", "Folder/anchor");
    expect(key).toBe("bn.synthesize.vault-1.Folder/anchor");

    const cfg = config();
    const model = createSynthesisModel(cfg);
    addSynthesisSource(model, cfg, candidate("one"));
    model.title = "Result";
    model.prompt = "  spaced prompt  ";
    model.draft = "line one\n\nline two\n";
    model.candidate = { markdown: "generated body", html: "<p>generated body</p>" };
    withContext(model);

    expect(writeSynthesisState(storage, key, synthesisStateFromModel(model))).toBe(true);
    const raw = String(storage.entries.get(key));
    expect(JSON.parse(raw)).toEqual({
      version: SYNTHESIS_STORAGE_VERSION,
      sources: ["one"],
      title: "Result",
      folder: "Synthesis",
      purpose: "synthesis",
      prompt: "  spaced prompt  ",
      draft: "line one\n\nline two\n",
      candidate: "generated body",
    });
    expect(raw).not.toContain("one detail");
    expect(raw).not.toContain("ctx-1");
    expect(raw).not.toContain("<p>");
  });

  it("restores text byte-for-byte and discards a foreign or malformed record", () => {
    const storage = memoryStorage();
    const key = synthesisStorageKey("vault-1", "anchor");

    storage.entries.set(
      key,
      JSON.stringify({
        version: SYNTHESIS_STORAGE_VERSION,
        sources: ["one", "two", "", null, { key: "three" }],
        title: " Result ",
        folder: "Synthesis",
        purpose: "questions",
        prompt: "  keep  ",
        draft: "  keep  ",
        candidate: "  candidate  ",
      })
    );
    expect(readSynthesisState(storage, key)).toEqual({
      sources: ["one", "two"],
      title: " Result ",
      folder: "Synthesis",
      purpose: "questions",
      prompt: "  keep  ",
      draft: "  keep  ",
      candidate: "  candidate  ",
    });

    storage.entries.set(key, JSON.stringify({ version: 0, sources: ["one"] }));
    expect(readSynthesisState(storage, key)).toBeNull();

    storage.entries.set(key, "{not json");
    expect(readSynthesisState(storage, key)).toBeNull();

    storage.entries.set(key, JSON.stringify(["not", "a", "record"]));
    expect(readSynthesisState(storage, key)).toBeNull();

    expect(clearSynthesisState(storage, key)).toBe(true);
    expect(readSynthesisState(storage, key)).toBeNull();
  });

  it("treats a throwing or absent storage as a non-fatal failure", () => {
    const throwing: SynthesisStorageLike = {
      getItem: () => {
        throw new Error("blocked");
      },
      setItem: () => {
        throw new Error("quota exceeded");
      },
      removeItem: () => {
        throw new Error("blocked");
      },
    };
    const state = synthesisStateFromModel(createSynthesisModel(config()));
    expect(readSynthesisState(throwing, "key")).toBeNull();
    expect(writeSynthesisState(throwing, "key", state)).toBe(false);
    expect(clearSynthesisState(throwing, "key")).toBe(false);

    expect(readSynthesisState(null, "key")).toBeNull();
    expect(writeSynthesisState(null, "key", state)).toBe(false);
    expect(clearSynthesisState(null, "key")).toBe(false);
    expect(safeSynthesisStorage(null)).toBeNull();
  });
});
