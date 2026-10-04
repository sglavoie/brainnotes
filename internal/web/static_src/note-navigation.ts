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

// Only propagate a validated Library destination to local note/editor routes.
const isContextDestination = (url: URL, allowEditor: boolean): boolean =>
  url.origin === window.location.origin && !url.username && !url.password &&
  (url.pathname.startsWith("/n/") || (allowEditor && url.pathname === "/edit"));

export const withLibraryContext = (target: string, allowEditor = false): string => {
  const path = libraryReturnPath();
  if (!path || target.startsWith("#")) return target;
  try {
    const url = new URL(target, window.location.origin);
    if (!isContextDestination(url, allowEditor)) return target;
    url.searchParams.set("return_to", path);
    return url.pathname + url.search + url.hash;
  } catch { return target; }
};

const carryLibraryContext = (): void => {
  const selector = "[data-note-context-links] a[href], a[data-source-editor-launch], a[data-source-note-return]";
  for (const link of document.querySelectorAll<HTMLAnchorElement>(selector)) {
    const target = link.getAttribute("href");
    if (target) link.setAttribute("href", withLibraryContext(target, link.hasAttribute("data-source-editor-launch")));
  }
};

export const initNoteNavigation = (): void => {
  const back = document.querySelector<HTMLAnchorElement>("[data-note-back-results]");
  const path = libraryReturnPath();
  if (path) carryLibraryContext();
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
