type ReadingPosition = { heading: string; offset: number; ratio: number };
const parameter = "read_at";

const capture = (body: HTMLElement): ReadingPosition => {
  let heading: HTMLElement | undefined;
  for (const candidate of body.querySelectorAll<HTMLElement>("h1[id], h2[id], h3[id], h4[id], h5[id], h6[id]")) {
    if (candidate.getBoundingClientRect().top <= 96) heading = candidate;
  }
  const height = Math.max(1, document.documentElement.scrollHeight - window.innerHeight);
  return { heading: heading?.id || "", offset: heading?.getBoundingClientRect().top || 0,
    ratio: Math.min(1, Math.max(0, window.scrollY / height)) };
};

const parsePosition = (raw: string | null): ReadingPosition | null => {
  if (!raw) return null;
  try {
    const position: ReadingPosition = JSON.parse(raw);
    if (typeof position.heading !== "string" || !Number.isFinite(position.offset) ||
        !Number.isFinite(position.ratio) || position.ratio < 0 || position.ratio > 1) return null;
    return position;
  } catch { return null; }
};

// Carry position in the editor URL so separate tabs and renamed notes retain it.
export const initReadingPosition = (): void => {
  const body = document.querySelector<HTMLElement>("[data-note-body]");
  const raw = new URLSearchParams(window.location.search).get(parameter);
  if (!body) {
    if (parsePosition(raw)) for (const link of document.querySelectorAll<HTMLAnchorElement>("[data-source-note-return]")) {
      const url = new URL(link.href);
      url.searchParams.set(parameter, raw!);
      link.href = url.href;
    }
    return;
  }
  for (const link of document.querySelectorAll<HTMLAnchorElement>("[data-source-editor-launch]")) {
    const update = (): void => {
      const url = new URL(link.href);
      url.searchParams.set(parameter, JSON.stringify(capture(body)));
      link.href = url.href;
    };
    for (const event of ["pointerdown", "focus", "click", "contextmenu"]) link.addEventListener(event, update);
  }
  const position = parsePosition(raw);
  if (!position || window.location.hash) return;
  let cancelled = false;
  const cancel = (): void => { cancelled = true; };
  const events = ["pointerdown", "keydown", "wheel", "touchstart"] as const;
  for (const event of events) document.addEventListener(event, cancel, { once: true, passive: true });
  window.requestAnimationFrame(() => window.requestAnimationFrame(() => {
    for (const event of events) document.removeEventListener(event, cancel);
    if (cancelled) return;
    const heading = document.getElementById(position.heading);
    const top = heading && body.contains(heading)
      ? window.scrollY + heading.getBoundingClientRect().top - position.offset
      : position.ratio * (document.documentElement.scrollHeight - window.innerHeight);
    window.scrollTo({ top: Math.max(0, top), behavior: "instant" });
    const url = new URL(window.location.href);
    url.searchParams.delete(parameter);
    window.history.replaceState(window.history.state, "", url);
  }));
};

export const carryReadingPosition = (target: string): string => {
  if (window.location.pathname !== "/edit") return target;
  const raw = new URLSearchParams(window.location.search).get(parameter);
  if (!parsePosition(raw)) return target;
  try {
    const url = new URL(target, window.location.origin);
    if (url.origin !== window.location.origin || (!url.pathname.startsWith("/n/") && url.pathname !== "/edit")) return target;
    url.searchParams.set(parameter, raw!);
    return url.pathname + url.search + url.hash;
  } catch { return target; }
};
