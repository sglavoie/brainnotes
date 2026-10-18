import { readSynthesisState, synthesisStorageKey, type SynthesisStorageLike, type SynthesisStoredState } from "./synthesize-storage";

// resume is only a reference to a browser-local copy. Every document writes a
// fresh key, including duplicated tabs and links opened in a new tab.
export const synthesisResume = (storage: SynthesisStorageLike | null, vault: string, anchor: string): SynthesisStoredState | null => {
  const id = new URLSearchParams(location.search).get("resume");
  if (!id || !/^[a-zA-Z0-9-]{1,80}$/.test(id)) return null;
  return readSynthesisState(storage, synthesisStorageKey(vault, anchor, id));
};

export const rememberSynthesisSession = (id: string): void => {
  if (location.pathname !== "/synthesize") return;
  const url = new URL(location.href);
  url.searchParams.set("resume", id);
  history.replaceState(history.state, "", url);
};

// Source rows arrive after hydration. Restore only after they exist, and do
// not steal focus if the user has already started interacting.
export const restoreSynthesisPosition = (root: HTMLElement): (() => void) => {
  const fragment = location.hash.slice(1);
  let pending = fragment.startsWith("bn-origin-");
  const cancel = (): void => { pending = false; };
  const events = ["pointerdown", "keydown", "wheel", "touchstart"] as const;
  for (const event of events) document.addEventListener(event, cancel, { once: true, passive: true });
  return (): void => {
    // Origin links are annotated by the shared navigation observer.
    requestAnimationFrame(() => {
      for (const event of events) document.removeEventListener(event, cancel);
      if (!pending) return;
      const link = root.ownerDocument.getElementById(fragment);
      if (!link || !root.contains(link)) return;
      for (const disclosure of ancestors(link)) disclosure.open = true;
      link.focus({ preventScroll: true });
      link.scrollIntoView({ block: "center" });
    });
  };
};

const ancestors = (element: HTMLElement): HTMLDetailsElement[] => {
  const result: HTMLDetailsElement[] = [];
  for (let parent = element.parentElement; parent; parent = parent.parentElement) {
    if (parent instanceof HTMLDetailsElement) result.push(parent);
  }
  return result;
};
