const gap = 10;
const clamp = (value: number, min: number, max: number): number => Math.max(min, Math.min(value, max));

// A selection can span more than the viewport. Try the side gutters in that
// case; if there is no free space, keep Alt+Enter available until it scrolls in.
const place = (toolbar: HTMLElement, rect: DOMRect): void => {
  const width = toolbar.offsetWidth;
  const height = toolbar.offsetHeight;
  const bottom = innerHeight - gap;
  let top = Math.min(rect.top - height - gap, bottom - height);
  let left = clamp(rect.left, gap, innerWidth - width - gap);
  if (top < gap) top = Math.max(gap, rect.bottom + gap);
  if (top + height > bottom || top < gap) {
    const available = Math.max(rect.left - 2 * gap, innerWidth - rect.right - 2 * gap);
    toolbar.style.maxWidth = `${Math.max(0, available)}px`;
    toolbar.dataset.side = "true";
    left = rect.left > innerWidth - rect.right ? rect.left - toolbar.offsetWidth - gap : rect.right + gap;
    top = clamp(rect.top, gap, bottom - toolbar.offsetHeight);
    if (toolbar.scrollWidth > toolbar.clientWidth || toolbar.offsetHeight > bottom - gap) {
      toolbar.style.opacity = "0"; toolbar.style.pointerEvents = "none"; toolbar.style.animation = "none";
    }
  }
  toolbar.style.left = `${left}px`;
  toolbar.style.top = `${top}px`;
};

export const trackPassagePosition = (toolbar: HTMLElement, getRange: () => Range | undefined): (() => void) => {
  const fine = matchMedia("(pointer: fine)");
  const update = (): void => {
    toolbar.removeAttribute("style"); delete toolbar.dataset.side;
    if (toolbar.hidden || !fine.matches) return;
    const range = getRange();
    if (!range) return;
    const rect = range.getBoundingClientRect();
    toolbar.style.bottom = "auto"; toolbar.style.transform = "none";
    place(toolbar, rect);
  };
  let frame = 0;
  const schedule = (): void => {
    cancelAnimationFrame(frame); frame = requestAnimationFrame(update);
  };
  toolbar.addEventListener("focusin", () => {
    if (toolbar.style.opacity !== "0") return;
    const rect = getRange()?.getBoundingClientRect();
    if (rect) window.scrollBy(0, rect.top - innerHeight / 2);
    update();
  });
  window.addEventListener("scroll", schedule, { passive: true, capture: true });
  window.addEventListener("resize", schedule);
  fine.addEventListener("change", schedule);
  const observer = new ResizeObserver(schedule);
  // Observe the reading area, not the bar whose side layout we change.
  const body = document.querySelector("[data-note-body]");
  if (body) observer.observe(body);
  return update;
};
