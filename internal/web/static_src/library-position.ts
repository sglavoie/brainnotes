const resultPrefix = "library-result-";

export const libraryResultID = (key: string): string => resultPrefix + encodeURIComponent(key);

const opensInSameTab = (event: MouseEvent): boolean =>
  event.button === 0 && !event.ctrlKey && !event.metaKey && !event.shiftKey && !event.altKey;

// The fragment travels inside return_to, so the position survives another tab,
// reloads and visits to other notes without retaining browser storage.
export const bindLibraryPosition = (list: HTMLElement): (() => void) => {
  if (window.location.pathname !== "/library") return () => {};
  const initialURL = window.location.href;
  const initialHash = window.location.hash.slice(1);
  let pending = initialHash.startsWith(resultPrefix);
  const cancel = (): void => { pending = false; };
  const events = ["pointerdown", "keydown", "wheel", "touchstart"] as const;
  if (pending) {
    for (const event of events) document.addEventListener(event, cancel, { once: true, passive: true });
  }

  list.addEventListener("click", (event) => {
    if (!opensInSameTab(event)) return;
    const target = event.target;
    if (!(target instanceof Element)) return;
    const link = target.closest<HTMLAnchorElement>(".overview-result-link");
    const row = link?.closest<HTMLElement>("[data-overview-result-key]");
    if (!row?.id || !list.contains(row)) return;
    const url = new URL(window.location.href);
    url.hash = row.id;
    // Replace, rather than push, so browser Back returns to this same search.
    window.history.replaceState(window.history.state, "", url);
  });

  return () => {
    for (const event of events) document.removeEventListener(event, cancel);
    const shouldRestore = pending && window.location.href === initialURL;
    pending = false;
    if (!shouldRestore) return;
    // Compare literal IDs: note keys can contain CSS selector punctuation.
    const row = Array.from(list.children).find((item) => item.id === initialHash);
    const link = row?.querySelector<HTMLAnchorElement>(".overview-result-link");
    if (!link) return;
    link.focus({ preventScroll: true });
    link.scrollIntoView({ block: "center", behavior: "instant" });
  };
};
