// One owner in app.js also serves lazily loaded switcher/composer bundles.
export const prepareTitleNavigation = (title: HTMLElement | null): void => {
  if (title) document.dispatchEvent(new CustomEvent("bn:title-navigation", { detail: title }));
};

const titleForLink = (link: HTMLAnchorElement): HTMLElement | null => {
  if (link.matches(".note-peek > a")) return document.querySelector("#note-peek-title");
  if (link.matches(".connection-saved-actions > a")) return link.closest(".connection-saved")?.querySelector("h3") || null;
  if (link.matches(".overview-result-link")) return link.querySelector(".overview-result-title");
  if (link.matches(".brief-card-title a")) return link.closest(".brief-card-title");
  return link.matches(".pinned-note-title, .recent-note-title") ? link : null;
};

const ordinaryActivation = (event: MouseEvent): boolean =>
  !event.defaultPrevented && !event.button && !event.metaKey && !event.ctrlKey && !event.altKey && !event.shiftKey;

const navigationLink = (event: MouseEvent): HTMLAnchorElement | null => {
  if (!ordinaryActivation(event)) return null;
  const link = event.target instanceof Element ? event.target.closest<HTMLAnchorElement>('a[href]') : null;
  if (!link || link.hasAttribute("download") || (link.target && link.target !== "_self")) return null;
  const url = new URL(link.href);
  return url.origin === location.origin && url.pathname.startsWith("/n/") ? link : null;
};

export const initTitleNavigation = (): void => {
  const motion = window.matchMedia("(prefers-reduced-motion: no-preference)");
  let restore: (() => void) | undefined;
  const reset = (): void => { restore?.(); restore = undefined; };
  const prepare = (title: HTMLElement): void => {
    reset();
    if (!motion.matches || !CSS.supports("view-transition-name", "thought-title")) return;
    const elements = new Set<HTMLElement>([...document.querySelectorAll<HTMLElement>(".reading-title, #source-editor-heading"), title]);
    const previous = [...elements].map((element) => [element, element.style.viewTransitionName] as const);
    for (const element of elements) element.style.viewTransitionName = element === title ? "thought-title" : "none";
    restore = () => { for (const [element, value] of previous) element.style.viewTransitionName = value; };
  };
  document.addEventListener("bn:title-navigation", (event) => prepare((event as CustomEvent<HTMLElement>).detail));
  document.addEventListener("keydown", reset, { capture: true });
  document.addEventListener("click", (event) => {
    reset();
    const link = navigationLink(event);
    if (!link) return;
    const title = titleForLink(link);
    if (!title) return;
    prepare(title);
    // Target/bubble handlers can cancel after this capture handler ran.
    queueMicrotask(() => { if (event.defaultPrevented) reset(); });
  }, { capture: true });
  window.addEventListener("pageshow", reset);
  window.addEventListener("popstate", reset);
  motion.addEventListener("change", reset);
};
