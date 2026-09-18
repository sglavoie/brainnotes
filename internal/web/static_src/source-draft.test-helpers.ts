import { draftSchemaVersion, type DraftStorage, type SourceDraft } from "./source-draft";

// A storage for the draft tests.
//
// It behaves like the real one in the two ways that matter: `key(index)` walks
// insertion order, and a write past `capacity` throws the way a browser does
// when the origin's quota is gone. It is a plain object rather than jsdom's
// localStorage because "this browser is full" and "this browser will not let
// you" are two of the states under test and neither is reachable in jsdom —
// and because a jsdom-only fake would leave the node-environment suites unable
// to use it at all.
export type FakeDraftStorage = DraftStorage & {
  capacity: number;
  // maxBytes is the other half of a real quota, and the half a count of records
  // cannot express: a browser measures characters, so replacing a record with a
  // longer one can be refused while the number of keys never moves. That is
  // exactly the write a restore makes into a slot this tab already had a record
  // in, so without it the state the restore has to survive is unreachable here.
  maxBytes: number;
  bytes(): number;
  keys(): string[];
};

export const memoryDraftStorage = (capacity = Number.POSITIVE_INFINITY): FakeDraftStorage => {
  const entries = new Map<string, string>();
  const bytes = (): number => {
    let total = 0;
    for (const [key, value] of entries) {
      total += key.length + value.length;
    }
    return total;
  };
  return {
    capacity,
    maxBytes: Number.POSITIVE_INFINITY,
    bytes,
    keys: () => [...entries.keys()],
    get length(): number {
      return entries.size;
    },
    key: (index: number) => [...entries.keys()][index] ?? null,
    getItem: (key: string) => entries.get(key) ?? null,
    setItem(key: string, value: string): void {
      if (!entries.has(key) && entries.size >= this.capacity) {
        throw new DOMException("quota", "QuotaExceededError");
      }
      const replaced = entries.get(key) ?? "";
      const grown = bytes() - replaced.length + value.length + (entries.has(key) ? 0 : key.length);
      if (grown > this.maxBytes) {
        throw new DOMException("quota", "QuotaExceededError");
      }
      entries.set(key, value);
    },
    removeItem: (key: string) => void entries.delete(key),
  };
};

// draftKey spells the store's key format out by hand. That duplication is the
// point: the shape is the contract — vault, note, tab — and a helper that asked
// the module for it would assert nothing.
export const draftKey = (vaultID: string, noteKey: string, draftID: string): string =>
  `bn.draft.${draftSchemaVersion}|${encodeURIComponent(vaultID)}|${encodeURIComponent(noteKey)}|${encodeURIComponent(draftID)}`;

export const savedKey = (vaultID: string, noteKey: string): string =>
  `bn.draft-saved.${draftSchemaVersion}|${encodeURIComponent(vaultID)}|${encodeURIComponent(noteKey)}`;

export const makeDraft = (overrides: Partial<SourceDraft> = {}): SourceDraft => ({
  version: draftSchemaVersion,
  vaultID: "sync:v1",
  noteKey: "alpha",
  relPath: "Alpha.md",
  baseRevision: "r1",
  draftID: "tab-1",
  content: "unsaved text\n",
  updatedAt: 1_000,
  ...overrides,
});

export const seedDraft = (storage: DraftStorage, overrides: Partial<SourceDraft> = {}): SourceDraft => {
  const record = makeDraft(overrides);
  storage.setItem(draftKey(record.vaultID, record.noteKey, record.draftID), JSON.stringify(record));
  return record;
};

export const readDrafts = (storage: FakeDraftStorage): SourceDraft[] =>
  storage
    .keys()
    .filter((key) => key.startsWith(`bn.draft.${draftSchemaVersion}|`))
    .map((key) => JSON.parse(storage.getItem(key) ?? "null") as SourceDraft);
