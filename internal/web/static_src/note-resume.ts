export type NoteResume = { sourceLine: number; label: string; excerpt: string; heading: string; offset: number; ratio: number };
const storageKey = (): string => `bn.resume.v1:${document.documentElement.dataset.vaultId || ""}`;
const keyFor = (key: string): string => key.normalize("NFC").toLowerCase().replace(/\.md$/i, "");

const readEntries = (): Record<string, NoteResume> => {
  try { return JSON.parse(window.sessionStorage.getItem(storageKey()) || "{}"); } catch { return {}; }
};

export const readNoteResume = (key: string): NoteResume | null => {
  const value = readEntries()?.[keyFor(key)];
  return value && Number.isSafeInteger(value.sourceLine) && value.sourceLine > 0 &&
    typeof value.label === "string" && typeof value.excerpt === "string" &&
    typeof value.heading === "string" && Number.isFinite(value.offset) && Number.isFinite(value.ratio)
    ? value : null;
};

export const rememberNoteResume = (key: string, value: NoteResume): void => {
  if (!document.documentElement.dataset.vaultId) return;
  try {
    const entries = readEntries();
    delete entries[keyFor(key)];
    const recent = Object.fromEntries(Object.entries(entries).slice(-31));
    recent[keyFor(key)] = { ...value, label: value.label.slice(0, 160), excerpt: value.excerpt.slice(0, 320) };
    window.sessionStorage.setItem(storageKey(), JSON.stringify(recent));
  } catch { /* Resuming is optional; existing draft recovery remains independent. */ }
};

export const resumeDestination = (target: string): string => {
  const url = new URL(target, window.location.origin);
  if (url.origin !== window.location.origin || url.pathname !== "/edit") return target;
  const resume = readNoteResume(url.searchParams.get("key") || "");
  if (resume) url.searchParams.set("read_at", JSON.stringify(resume));
  return url.pathname + url.search + url.hash;
};

// Construct the one additional navigation the Brief supports, keeping all
// other Open consumers restricted to rendered notes.
export const briefEditorDestination = (target: string): string => {
  if (!target.startsWith("/edit?")) return "";
  const key = new URLSearchParams(target.slice(6)).get("key");
  return key ? `/edit?${new URLSearchParams({ key })}` : "";
};
