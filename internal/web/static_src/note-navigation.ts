// Carry the Library URL in the link itself so reloads and new tabs retain it.
export const libraryResultPath = (path: string): string => {
  if (window.location.pathname !== "/library" || !path.startsWith("/n/")) return path;
  const url = new URL(path, window.location.origin);
  url.searchParams.set("return_to", window.location.pathname + window.location.search + window.location.hash);
  return url.pathname + url.search + url.hash;
};

const libraryReturnPath = (): string | null => {
  const value = new URLSearchParams(window.location.search).get("return_to");
  if (!value?.startsWith("/")) return null;
  try {
    const url = new URL(value, window.location.origin);
    if (url.origin !== window.location.origin || url.pathname !== "/library" || url.username || url.password) return null;
    return url.pathname + url.search + url.hash;
  } catch {
    return null;
  }
};

export const initNoteNavigation = (): void => {
  const back = document.querySelector<HTMLAnchorElement>("[data-note-back-results]");
  const path = libraryReturnPath();
  if (back && path) {
    back.href = path;
    back.hidden = false;
  }
  const copy = document.querySelector<HTMLButtonElement>("[data-copy-note-url]");
  if (copy) {
    const url = new URL(window.location.href);
    url.search = "";
    url.hash = "";
    copy.dataset.copyText = url.href;
    copy.hidden = false;
  }
};
