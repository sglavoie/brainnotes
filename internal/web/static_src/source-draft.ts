// Where the editor's unsaved text goes when the tab, the network or the server
// disappears.
//
// The store is deliberately dumb: it writes a record, lists the records for one
// note, and removes them. Every decision about what a record *means* — whether
// it is still current, whether the note it names still exists, whether it may be
// offered back — is in source-draft-recovery.ts, because those are answers about
// the vault and this module has never spoken to one.
//
// Three properties are load-bearing.
//
// The key carries the **vault identity**. `http://127.0.0.1:8765` is one storage
// origin that a person points at several vaults over a machine's life, and a
// draft for `Alpha.md` in one of them must never be offered over `Alpha.md` in
// another. The identity comes from the server (see internal/web/vault_identity.go)
// and is opaque here.
//
// The key carries a **tab identity**. Two tabs editing one note are two drafts,
// not one: a shared slot would mean the tab that stopped typing first has its
// words overwritten by the one still going, which is precisely the loss this
// exists to prevent.
//
// And **no failure throws**. Storage can be absent, disabled, or full, and an
// editor that broke because a browser declined to keep a copy would have turned
// a missing safety net into the accident.

export const draftSchemaVersion = 1;

const draftKeyPrefix = `bn.draft.${draftSchemaVersion}|`;
const savedKeyPrefix = `bn.draft-saved.${draftSchemaVersion}|`;

// defaultMaxAgeMs is how long an unclaimed draft is worth offering. A draft is
// somebody's unfinished sentence, not an archive; past a fortnight the note it
// was written against has usually moved on, and every stale record left behind
// is one more thing between a person and the one they actually want back.
const defaultMaxAgeMs = 14 * 24 * 60 * 60 * 1000;

export type SourceDraft = {
  version: number;
  vaultID: string;
  noteKey: string;
  relPath: string;
  // baseRevision is the revision the draft was started from. It is what makes a
  // recovered draft answerable: without it nothing can tell text the vault has
  // moved past from text it is still waiting for.
  baseRevision: string;
  draftID: string;
  content: string;
  updatedAt: number;
};

// DraftWriteOutcome distinguishes the two ways a draft is not kept, because they
// need different sentences. "unavailable" is a browser that never offered
// storage; "refused" is one that has it and would not take this.
export type DraftWriteOutcome = "stored" | "unavailable" | "refused";

// DraftStorage is the slice of the Storage interface this module uses. Taking
// the narrow shape rather than `Storage` is what lets a test hand over a map and
// a full disk without a jsdom quirk in the way.
export type DraftStorage = {
  readonly length: number;
  key(index: number): string | null;
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
};

export type SourceDraftStoreOptions = {
  vaultID: string;
  noteKey: string;
  draftID: string;
  storage: DraftStorage | null;
  now?: () => number;
  maxAgeMs?: number;
};

export type SourceDraftStore = {
  readonly draftID: string;
  available(): boolean;
  // keep writes this tab's draft for this note. It overwrites its own slot and
  // no other tab's.
  keep(input: { relPath: string; baseRevision: string; content: string }): DraftWriteOutcome;
  // drop removes one draft — this tab's when no id is given.
  drop(draftID?: string): void;
  // list is every draft for this vault and this note, newest first, this tab's
  // included.
  list(): SourceDraft[];
  // announceSaved tells the other tabs on this origin that the note now holds a
  // new revision. It is a notification and never an instruction: a tab that
  // receives one still holds its own unsaved text and still decides for itself.
  announceSaved(revision: string): void;
  // peerSavedRevision reads a `storage` event: the revision when the event is
  // this note's announcement, null for every other key.
  peerSavedRevision(key: string | null, newValue: string | null): string | null;
};

// resolveDraftStorage answers what this browser will actually give us.
//
// The access itself is inside the try, not only the call: reading
// `window.localStorage` throws outright when storage is disabled by policy, so a
// guard that only wrapped `setItem` would fail at the property lookup instead.
export const resolveDraftStorage = (view: Window | undefined = globalThis.window): DraftStorage | null => {
  try {
    const storage = view?.localStorage;
    if (!storage) {
      return null;
    }
    // A probe, because presence is not permission: Safari's private browsing
    // used to expose a localStorage whose every write threw, and a store that
    // reported itself available there would promise to keep text it drops.
    const probe = `${draftKeyPrefix}probe`;
    storage.setItem(probe, "1");
    storage.removeItem(probe);
    return storage;
  } catch {
    return null;
  }
};

// resolveDraftTabID gives this tab its identity, and gives it back after a
// reload.
//
// sessionStorage is exactly the right lifetime: it is per tab and it survives a
// reload, which is the interruption this whole feature is most often answering.
// When it is unavailable the id is minted per page load instead, and nothing
// breaks — the previous load's draft is simply listed as one more recoverable
// draft rather than as this tab's own, and is still offered back.
export const resolveDraftTabID = (
  newID: () => string,
  view: Window | undefined = globalThis.window
): string => {
  const key = `bn.draft-tab.${draftSchemaVersion}`;
  try {
    const storage = view?.sessionStorage;
    if (storage) {
      const existing = storage.getItem(key);
      if (existing) {
        return existing;
      }
      const minted = newID();
      storage.setItem(key, minted);
      return minted;
    }
  } catch {
    // Fall through to an ephemeral id.
  }
  return newID();
};

export const createSourceDraftStore = (options: SourceDraftStoreOptions): SourceDraftStore => {
  const storage = options.storage;
  const now = options.now ?? (() => Date.now());
  const maxAgeMs = options.maxAgeMs ?? defaultMaxAgeMs;
  const notePrefix = `${draftKeyPrefix}${encode(options.vaultID)}|${encode(options.noteKey)}|`;
  const ownKey = `${notePrefix}${encode(options.draftID)}`;
  const savedKey = `${savedKeyPrefix}${encode(options.vaultID)}|${encode(options.noteKey)}`;

  const write = (key: string, value: string): DraftWriteOutcome => {
    if (!storage) {
      return "unavailable";
    }
    try {
      storage.setItem(key, value);
      return "stored";
    } catch {
      // A quota failure is usually somebody else's abandoned draft, so make room
      // once and try again. Anything still refused after that is reported rather
      // than retried: a loop that keeps deleting to fit one oversized note would
      // trade every other note's text for this one's.
      return evictOldestForeignDraft(storage, ownKey) && retry(storage, key, value)
        ? "stored"
        : "refused";
    }
  };

  if (storage) {
    pruneExpiredDrafts(storage, now() - maxAgeMs);
  }

  return {
    draftID: options.draftID,
    available: () => storage !== null,

    keep(input): DraftWriteOutcome {
      const draft: SourceDraft = {
        version: draftSchemaVersion,
        vaultID: options.vaultID,
        noteKey: options.noteKey,
        relPath: input.relPath,
        baseRevision: input.baseRevision,
        draftID: options.draftID,
        content: input.content,
        updatedAt: now(),
      };
      return write(ownKey, JSON.stringify(draft));
    },

    drop(draftID?: string): void {
      remove(storage, draftID === undefined ? ownKey : `${notePrefix}${encode(draftID)}`);
    },

    list(): SourceDraft[] {
      return readDrafts(storage, notePrefix).sort((left, right) => right.updatedAt - left.updatedAt);
    },

    announceSaved(revision: string): void {
      // Deliberately not routed through `write`: an announcement is a courtesy
      // to another tab and must never evict a draft to make room for itself.
      if (!storage) {
        return;
      }
      try {
        storage.setItem(savedKey, JSON.stringify({ revision, at: now() }));
      } catch {
        // A tab that is not told simply finds out at its next save, which is the
        // behaviour before any of this existed.
      }
    },

    peerSavedRevision(key: string | null, newValue: string | null): string | null {
      if (key !== savedKey || !newValue) {
        return null;
      }
      try {
        const parsed: unknown = JSON.parse(newValue);
        const revision = (parsed as { revision?: unknown } | null)?.revision;
        return typeof revision === "string" && revision !== "" ? revision : null;
      } catch {
        return null;
      }
    },
  };
};

const retry = (storage: DraftStorage, key: string, value: string): boolean => {
  try {
    storage.setItem(key, value);
    return true;
  } catch {
    return false;
  }
};

const remove = (storage: DraftStorage | null, key: string): void => {
  if (!storage) {
    return;
  }
  try {
    storage.removeItem(key);
  } catch {
    // A removal that fails leaves a draft to be offered again. That is a
    // nuisance and never a loss, so it is not worth a sentence on the page.
  }
};

// draftKeys snapshots the matching keys before anything is removed. Iterating
// `storage.key(i)` while deleting reindexes the store underneath the loop, which
// silently skips entries — the classic way a prune leaves half its work behind.
const draftKeys = (storage: DraftStorage, prefix: string): string[] => {
  const keys: string[] = [];
  try {
    for (let index = 0; index < storage.length; index += 1) {
      const key = storage.key(index);
      if (key !== null && key.startsWith(prefix)) {
        keys.push(key);
      }
    }
  } catch {
    return keys;
  }
  return keys;
};

const readDrafts = (storage: DraftStorage | null, prefix: string): SourceDraft[] => {
  if (!storage) {
    return [];
  }
  const drafts: SourceDraft[] = [];
  for (const key of draftKeys(storage, prefix)) {
    const draft = parseDraft(safeGet(storage, key));
    if (draft) {
      drafts.push(draft);
    }
  }
  return drafts;
};

const safeGet = (storage: DraftStorage, key: string): string | null => {
  try {
    return storage.getItem(key);
  } catch {
    return null;
  }
};

// parseDraft is the only door a stored record comes through, and it trusts
// nothing: local storage is writable by anything else on the origin and by the
// person themselves, and a record from a future schema is a stranger too.
const parseDraft = (raw: string | null): SourceDraft | null => {
  if (!raw) {
    return null;
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return null;
  }
  const record = parsed as Partial<SourceDraft> | null;
  if (!record || record.version !== draftSchemaVersion) {
    return null;
  }
  if (typeof record.content !== "string" || typeof record.draftID !== "string") {
    return null;
  }
  return {
    version: draftSchemaVersion,
    vaultID: text(record.vaultID),
    noteKey: text(record.noteKey),
    relPath: text(record.relPath),
    baseRevision: text(record.baseRevision),
    draftID: record.draftID,
    content: record.content,
    updatedAt: typeof record.updatedAt === "number" && Number.isFinite(record.updatedAt) ? record.updatedAt : 0,
  };
};

const text = (value: unknown): string => (typeof value === "string" ? value : "");

// pruneExpiredDrafts also collects records this build cannot read at all — a
// truncated write, a schema that moved on and back. They can never be offered,
// so leaving them is nothing but quota somebody else's draft needs.
const pruneExpiredDrafts = (storage: DraftStorage, before: number): void => {
  for (const key of draftKeys(storage, draftKeyPrefix)) {
    const draft = parseDraft(safeGet(storage, key));
    if (!draft || draft.updatedAt < before) {
      remove(storage, key);
    }
  }
};

// evictOldestForeignDraft frees room for the draft being written by dropping the
// oldest one that is not it. `except` is excluded by key rather than by age
// because the record being replaced may well be the oldest one there is, and
// deleting it to make room for itself would leave the note with nothing.
const evictOldestForeignDraft = (storage: DraftStorage, except: string): boolean => {
  let oldestKey: string | null = null;
  let oldestAt = Number.POSITIVE_INFINITY;
  for (const key of draftKeys(storage, draftKeyPrefix)) {
    if (key === except) {
      continue;
    }
    const draft = parseDraft(safeGet(storage, key));
    const at = draft ? draft.updatedAt : -1;
    if (at < oldestAt) {
      oldestAt = at;
      oldestKey = key;
    }
  }
  if (oldestKey === null) {
    return false;
  }
  remove(storage, oldestKey);
  return true;
};

// encode keeps the separator out of the components. A note key is derived from a
// vault-relative path and `|` is a legal character in one, so an unescaped key
// could be made to read as a different note's — or a different vault's — slot.
const encode = (value: string): string => encodeURIComponent(value);
