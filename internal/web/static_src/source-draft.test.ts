import { describe, expect, it, vi } from "vitest";

import {
  createSourceDraftStore,
  draftSchemaVersion,
  resolveDraftStorage,
  resolveDraftTabID,
  type DraftStorage,
} from "./source-draft";
import { draftKey, memoryDraftStorage, savedKey, seedDraft } from "./source-draft.test-helpers";

const store = (storage: DraftStorage | null, overrides: Partial<Parameters<typeof createSourceDraftStore>[0]> = {}) =>
  createSourceDraftStore({
    vaultID: "sync:v1",
    noteKey: "alpha",
    draftID: "tab-1",
    storage,
    now: () => 10_000,
    ...overrides,
  });

describe("createSourceDraftStore keys", () => {
  // The key shape is the feature. Every one of its three components answers a
  // way this could hand somebody the wrong text back: the vault because one
  // origin serves several over a machine's life, the note because the origin
  // holds all of them, and the tab because two windows on one note are two
  // drafts.
  it("writes a draft under the vault, the note and the tab", () => {
    const storage = memoryDraftStorage();
    expect(store(storage).keep({ relPath: "Alpha.md", baseRevision: "r1", content: "mine\n" })).toBe("stored");

    expect(storage.keys()).toEqual([draftKey("sync:v1", "alpha", "tab-1")]);
    const kept = store(storage).list();
    expect(kept).toHaveLength(1);
    expect(kept[0]).toMatchObject({
      vaultID: "sync:v1",
      noteKey: "alpha",
      relPath: "Alpha.md",
      baseRevision: "r1",
      draftID: "tab-1",
      content: "mine\n",
      updatedAt: 10_000,
    });
  });

  // One storage origin, several vaults. A draft for `Alpha.md` in the vault on
  // the Pi must not be offered over `Alpha.md` in a vault on this laptop — that
  // is the one way this feature could put somebody's words into a file they were
  // never written for.
  it("never lists a draft from another vault or another note", () => {
    const storage = memoryDraftStorage();
    seedDraft(storage, { draftID: "tab-9", vaultID: "root:other", content: "other vault\n" });
    seedDraft(storage, { draftID: "tab-9", noteKey: "beta", content: "other note\n" });
    store(storage).keep({ relPath: "Alpha.md", baseRevision: "r1", content: "mine\n" });

    expect(store(storage).list().map((draft) => draft.content)).toEqual(["mine\n"]);
  });

  // `|` is the separator and a legal character in a note key, which is derived
  // from a vault-relative path. Unescaped, `alpha|b` + tab `c` and `alpha` + tab
  // `b|c` are the same string, so one note's draft would be read as another's.
  it("keeps the separator out of the components", () => {
    const storage = memoryDraftStorage();
    store(storage, { noteKey: "alpha|b", draftID: "c" }).keep({
      relPath: "A.md",
      baseRevision: "r1",
      content: "left\n",
    });
    store(storage, { noteKey: "alpha", draftID: "b|c" }).keep({
      relPath: "A.md",
      baseRevision: "r1",
      content: "right\n",
    });

    expect(storage.keys()).toHaveLength(2);
    expect(store(storage, { noteKey: "alpha|b", draftID: "c" }).list().map((d) => d.content)).toEqual(["left\n"]);
    expect(store(storage, { noteKey: "alpha", draftID: "b|c" }).list().map((d) => d.content)).toEqual(["right\n"]);
  });
});

describe("createSourceDraftStore tabs", () => {
  it("lists every tab's draft for this note, newest first, and overwrites only its own", () => {
    const storage = memoryDraftStorage();
    seedDraft(storage, { draftID: "tab-2", content: "the other tab\n", updatedAt: 9_000 });
    store(storage).keep({ relPath: "Alpha.md", baseRevision: "r1", content: "this tab\n" });

    expect(store(storage).list().map((draft) => draft.draftID)).toEqual(["tab-1", "tab-2"]);
    expect(store(storage).list().map((draft) => draft.content)).toEqual(["this tab\n", "the other tab\n"]);
  });

  it("drops its own slot by default and a named one on request", () => {
    const storage = memoryDraftStorage();
    seedDraft(storage, { draftID: "tab-2", updatedAt: 9_000 });
    const keeper = store(storage);
    keeper.keep({ relPath: "Alpha.md", baseRevision: "r1", content: "this tab\n" });

    keeper.drop();
    expect(keeper.list().map((draft) => draft.draftID)).toEqual(["tab-2"]);
    keeper.drop("tab-2");
    expect(keeper.list()).toEqual([]);
  });
});

describe("createSourceDraftStore without usable storage", () => {
  // The whole module's contract in one test: a browser that will not keep a copy
  // is a browser with no safety net, never a broken editor.
  it("reports every operation as a no-op rather than throwing", () => {
    const keeper = store(null);

    expect(keeper.available()).toBe(false);
    expect(keeper.keep({ relPath: "Alpha.md", baseRevision: "r1", content: "mine\n" })).toBe("unavailable");
    expect(keeper.list()).toEqual([]);
    expect(() => keeper.drop()).not.toThrow();
    expect(() => keeper.announceSaved("r2")).not.toThrow();
  });

  it("survives a storage that throws on every call", () => {
    const hostile: DraftStorage = {
      get length(): number {
        throw new Error("denied");
      },
      key: () => {
        throw new Error("denied");
      },
      getItem: () => {
        throw new Error("denied");
      },
      setItem: () => {
        throw new Error("denied");
      },
      removeItem: () => {
        throw new Error("denied");
      },
    };
    const keeper = store(hostile);

    expect(keeper.available()).toBe(true);
    expect(keeper.keep({ relPath: "Alpha.md", baseRevision: "r1", content: "mine\n" })).toBe("refused");
    expect(keeper.list()).toEqual([]);
    expect(() => keeper.drop()).not.toThrow();
  });
});

describe("createSourceDraftStore under quota pressure", () => {
  it("makes room once by dropping the oldest draft that is not this one", () => {
    const storage = memoryDraftStorage(1);
    seedDraft(storage, { draftID: "tab-9", noteKey: "beta", updatedAt: 9_000 });

    expect(store(storage).keep({ relPath: "Alpha.md", baseRevision: "r1", content: "mine\n" })).toBe("stored");
    expect(storage.keys()).toEqual([draftKey("sync:v1", "alpha", "tab-1")]);
  });

  // Evicting once and reporting the refusal is the deliberate limit: a loop that
  // kept deleting until one oversized note fit would trade every other note's
  // text for this one's.
  it("reports a refusal rather than clearing the store for one draft", () => {
    const storage = memoryDraftStorage();
    seedDraft(storage, { draftID: "tab-8", noteKey: "beta", updatedAt: 8_000 });
    seedDraft(storage, { draftID: "tab-9", noteKey: "gamma", updatedAt: 9_000 });
    // Full as of now: one eviction is not enough to fit a third record.
    storage.capacity = 1;

    expect(store(storage).keep({ relPath: "Alpha.md", baseRevision: "r1", content: "mine\n" })).toBe("refused");
    expect(storage.keys()).toEqual([draftKey("sync:v1", "gamma", "tab-9")]);
  });

  // The record being replaced may well be the oldest one there is. Deleting it
  // to make room for itself would leave the note with nothing at all.
  it("never evicts its own slot to make room for its own draft", () => {
    const storage = memoryDraftStorage(0);

    expect(store(storage).keep({ relPath: "Alpha.md", baseRevision: "r1", content: "mine\n" })).toBe("refused");
    expect(storage.keys()).toEqual([]);
  });

  // An announcement is a courtesy to another tab. Letting it evict would trade
  // somebody's unsaved words for a notification.
  it("never evicts anything to announce a save", () => {
    const storage = memoryDraftStorage(1);
    seedDraft(storage, { draftID: "tab-9", noteKey: "beta", updatedAt: 9_000 });

    expect(() => store(storage).announceSaved("r2")).not.toThrow();
    expect(storage.keys()).toEqual([draftKey("sync:v1", "beta", "tab-9")]);
  });
});

describe("createSourceDraftStore pruning", () => {
  it("collects expired drafts and records it cannot read, from every vault", () => {
    const storage = memoryDraftStorage();
    seedDraft(storage, { draftID: "fresh", noteKey: "beta", updatedAt: 9_999 });
    seedDraft(storage, { draftID: "ancient", noteKey: "beta", updatedAt: 1 });
    seedDraft(storage, { draftID: "elsewhere", vaultID: "root:other", updatedAt: 1 });
    storage.setItem(draftKey("sync:v1", "beta", "truncated"), "{not json");
    storage.setItem(
      draftKey("sync:v1", "beta", "newer-schema"),
      JSON.stringify({ version: draftSchemaVersion + 1, draftID: "newer-schema", content: "x" })
    );
    // Not this module's key space, and not this module's business.
    storage.setItem("bn.theme", "dark");

    store(storage, { maxAgeMs: 5_000 });

    expect(storage.keys()).toEqual([draftKey("sync:v1", "beta", "fresh"), "bn.theme"]);
  });
});

describe("peerSavedRevision", () => {
  it("reads this note's announcement and ignores every other key", () => {
    const storage = memoryDraftStorage();
    const keeper = store(storage);
    keeper.announceSaved("r2");

    const key = savedKey("sync:v1", "alpha");
    expect(storage.getItem(key)).not.toBeNull();
    expect(keeper.peerSavedRevision(key, storage.getItem(key))).toBe("r2");
    expect(keeper.peerSavedRevision(key, null)).toBeNull();
    expect(keeper.peerSavedRevision(key, "{not json")).toBeNull();
    expect(keeper.peerSavedRevision(key, JSON.stringify({ revision: "" }))).toBeNull();
    expect(keeper.peerSavedRevision("bn.theme", "dark")).toBeNull();
    // Another note's announcement on the same origin.
    expect(
      keeper.peerSavedRevision(
        savedKey("sync:v1", "beta"),
        JSON.stringify({ revision: "r7" })
      )
    ).toBeNull();
  });
});

describe("resolveDraftStorage", () => {
  it("is null when there is no window and when the property itself throws", () => {
    expect(resolveDraftStorage(undefined)).toBeNull();

    // Storage disabled by policy throws at the *property access*, which is why
    // the access is inside the try rather than only the write.
    const denied = {
      get localStorage(): DraftStorage {
        throw new DOMException("denied", "SecurityError");
      },
    } as unknown as Window;
    expect(resolveDraftStorage(denied)).toBeNull();
  });

  // Presence is not permission: a private-browsing localStorage whose every
  // write throws would otherwise be reported as available, and the store would
  // promise to keep text it drops.
  it("is null for a storage that accepts nothing, and cleans up its probe", () => {
    const refusing = memoryDraftStorage(0);
    expect(resolveDraftStorage({ localStorage: refusing } as unknown as Window)).toBeNull();

    const working = memoryDraftStorage();
    expect(resolveDraftStorage({ localStorage: working } as unknown as Window)).toBe(working);
    expect(working.keys()).toEqual([]);
  });
});

describe("resolveDraftTabID", () => {
  it("gives a reloaded tab the identity it had before", () => {
    const session = memoryDraftStorage();
    const view = { sessionStorage: session } as unknown as Window;
    const mint = vi.fn(() => "tab-minted");

    expect(resolveDraftTabID(mint, view)).toBe("tab-minted");
    expect(resolveDraftTabID(mint, view)).toBe("tab-minted");
    expect(mint).toHaveBeenCalledTimes(1);
  });

  // Without session storage the id is per page load. The previous load's draft
  // is then listed as one more recoverable draft rather than as this tab's own,
  // which is a less precise offer and never a lost one.
  it("falls back to an ephemeral id when session storage is unusable", () => {
    const denied = {
      get sessionStorage(): DraftStorage {
        throw new DOMException("denied", "SecurityError");
      },
    } as unknown as Window;
    let counter = 0;
    const mint = () => `tab-${(counter += 1)}`;

    expect(resolveDraftTabID(mint, denied)).toBe("tab-1");
    expect(resolveDraftTabID(mint, denied)).toBe("tab-2");
    expect(resolveDraftTabID(mint, undefined)).toBe("tab-3");
  });
});
