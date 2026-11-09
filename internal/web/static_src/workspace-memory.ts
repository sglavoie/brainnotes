// Optional browser-local continuity. Drafts and write receipts use their own
// storage and must never be removed with reading history.
type MemoryName = "resume.v1" | "reference" | "switcher.recent";
const keyFor = (name: MemoryName): string | null => {
  const vault = document.documentElement.dataset.vaultId;
  return vault ? `bn.${name}:${vault}` : null;
};

export const readWorkspaceMemory = (name: MemoryName): string | null => {
  const key = keyFor(name);
  if (!key) return null;
  const previous = readStorage("sessionStorage", key);
  if (previous !== null) {
    if (store("localStorage", key, previous)) store("sessionStorage", key, null);
    return previous;
  }
  return readStorage("localStorage", key);
};

type StorageName = "localStorage" | "sessionStorage";
const readStorage = (storage: StorageName, key: string): string | null => {
  try { return window[storage].getItem(key); } catch { return null; }
};
const store = (storage: StorageName, key: string, value: string | null): boolean => {
  try {
    if (value === null) window[storage].removeItem(key);
    else window[storage].setItem(key, value);
    return true;
  } catch { return false; }
};

export const writeWorkspaceMemory = (name: MemoryName, value: string | null): boolean => {
  const key = keyFor(name);
  if (!key) return false;
  const durable = store("localStorage", key, value);
  const session = store("sessionStorage", key, durable ? null : value);
  return value === null ? durable && session : durable || session;
};

export const initWorkspaceMemory = (): void => {
  window.addEventListener("storage", (event) => {
    if (event.key === keyFor("resume.v1") && event.newValue === null) document.dispatchEvent(new Event("bn:forget-workspace"));
  });
  for (const button of document.querySelectorAll<HTMLButtonElement>("[data-forget-workspace]")) {
    button.addEventListener("click", () => {
      const cleared = (["resume.v1", "reference", "switcher.recent"] as const).map((name) => writeWorkspaceMemory(name, null));
      document.dispatchEvent(new Event("bn:forget-workspace"));
      const status = button.parentElement?.querySelector<HTMLElement>("[role=status]");
      if (status) status.textContent = cleared.every(Boolean)
        ? "Remembered places cleared for this vault in this browser."
        : "Browser storage is unavailable. Remembered places could not be fully cleared.";
    });
  }
};
