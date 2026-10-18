export const noteOrigins: Record<string, string> = {
  "/library": "results", "/pinned": "Pinned", "/": "Brief", "/graph": "Graph", "/synthesize": "Synthesis",
};

// Put the origin in actual links, including asynchronously rendered Brief and
// Graph links, so keyboard activation and opening a new tab work equally well.
export const bindNoteOrigin = (): void => {
  const path = window.location.pathname;
  if (!(path in noteOrigins) || path === "/library") return;
  const update = (): void => {
    for (const link of document.querySelectorAll<HTMLAnchorElement>("a[href]")) {
      const url = new URL(link.href);
      if (url.origin !== window.location.origin || !url.pathname.startsWith("/n/")) continue;
      if (!link.id) link.id = originLinkID(url.pathname);
      const origin = new URL(window.location.href);
      origin.hash = link.id;
      url.searchParams.set("return_to", origin.pathname + origin.search + origin.hash);
      if (link.href !== url.href) link.href = url.href;
    }
  };
  update();
  const observer = new MutationObserver(update);
  observer.observe(document.body, { subtree: true, childList: true, attributes: true, attributeFilter: ["href"] });
  window.addEventListener("pagehide", () => observer.disconnect(), { once: true });
};

export const validNoteOrigin = (url: URL): boolean => {
  if (url.origin !== window.location.origin || url.username || url.password || !(url.pathname in noteOrigins)) return false;
  return url.pathname !== "/synthesize" || (url.searchParams.getAll("source").length === 1 && !!url.searchParams.get("source")?.trim());
};

export const withNoteOrigin = (target: string): string => {
  if (!(window.location.pathname in noteOrigins)) return target;
  try {
    const url = new URL(target, window.location.origin);
    if (url.origin !== window.location.origin || !url.pathname.startsWith("/n/")) return target;
    url.searchParams.set("return_to", window.location.pathname + window.location.search + window.location.hash);
    return url.pathname + url.search + url.hash;
  } catch { return target; }
};

const originLinkID = (pathname: string): string => {
  const base = "bn-origin-" + encodeURIComponent(pathname);
  let id = base;
  let duplicate = 1;
  while (document.getElementById(id)) id = `${base}-${duplicate++}`;
  return id;
};
