import { asRecord, readCount } from "./api-internal";
import type { SynthesisModel } from "./synthesize-model";

export const SYNTHESIS_STORAGE_VERSION = 2;

export type SynthesisStorageLike = Pick<Storage, "getItem" | "setItem" | "removeItem"> & Partial<Pick<Storage, "key" | "length">>;

// Only note keys, the text the user typed, and the generated candidate are
// stored. Source bodies, excerpts, and the context packet stay in memory: they
// are complete note contents, and the workflow contract keeps them out of
// browser storage. The candidate's rendered HTML is deliberately not stored —
// it is derived from the Markdown that is, and restoring it would mean writing
// stored markup back into the page.
export type SynthesisStoredState = {
  sources: string[];
  title: string;
  folder: string;
  purpose: string;
  prompt: string;
  draft: string;
  candidate: string;
};

export const synthesisStorageKey = (vaultID: string, anchorKey: string, draftID?: string): string =>
  draftID ? `bn.synthesis-draft.${JSON.stringify([vaultID, anchorKey])}.${draftID}` : `bn.synthesize.${vaultID}.${anchorKey}`;

export const safeSynthesisStorage = (
  view: Window | null | undefined
): SynthesisStorageLike | null => {
  if (!view) {
    return null;
  }
  try {
    return view.localStorage;
  } catch {
    return null;
  }
};

// Draft and prompt text is kept byte-for-byte; trimming it would silently edit
// what the user wrote between page loads.
const readText = (value: unknown): string => (typeof value === "string" ? value : "");

// A stored key is only ever a string the app wrote. Coercing anything else
// would invent a key that resolves to nothing and then reports itself as a
// note the vault lost.
const readKeys = (value: unknown): string[] =>
  Array.isArray(value)
    ? value.flatMap((item) => {
        const key = typeof item === "string" ? item.trim() : "";
        return key === "" ? [] : [key];
      })
    : [];

export const synthesisStateFromModel = (model: SynthesisModel): SynthesisStoredState => ({
  sources: model.sources.map((source) => source.key),
  title: model.title,
  folder: model.folder,
  purpose: model.purpose,
  prompt: model.prompt,
  draft: model.draft,
  candidate: model.candidate ? model.candidate.markdown : "",
});

export const readSynthesisState = (
  storage: SynthesisStorageLike | null,
  key: string
): SynthesisStoredState | null => {
  if (!storage) {
    return null;
  }
  let raw: string | null;
  try {
    raw = storage.getItem(key);
  } catch {
    return null;
  }
  if (!raw) {
    return null;
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return null;
  }
  const record = asRecord(parsed);
  if (!record || readCount(record.version) !== SYNTHESIS_STORAGE_VERSION) {
    return null;
  }
  return {
    sources: readKeys(record.sources),
    title: readText(record.title),
    folder: readText(record.folder),
    purpose: readText(record.purpose),
    prompt: readText(record.prompt),
    draft: readText(record.draft),
    candidate: readText(record.candidate),
  };
};

export const writeSynthesisState = (
  storage: SynthesisStorageLike | null,
  key: string,
  state: SynthesisStoredState
): boolean => {
  if (!storage) {
    return false;
  }
  try {
    storage.setItem(key, JSON.stringify({ version: SYNTHESIS_STORAGE_VERSION, ...state }));
    return true;
  } catch {
    return false;
  }
};

export const clearSynthesisState = (
  storage: SynthesisStorageLike | null,
  key: string
): boolean => {
  if (!storage) {
    return false;
  }
  try {
    storage.removeItem(key);
    return true;
  } catch {
    return false;
  }
};
