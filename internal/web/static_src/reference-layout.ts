// Keep the visible passage anchored while a reference changes the page width.
export const preserveReadingPlace = (change: () => void): void => {
  const blocks = Array.from(document.querySelectorAll<HTMLElement>("[data-note-body] [data-source-line], [data-note-body] > p, [data-note-body] > ul"));
  const anchor = blocks.find((block) => block.getBoundingClientRect().bottom > 12 && block.getBoundingClientRect().top < innerHeight);
  const top = anchor?.getBoundingClientRect().top;
  change();
  if (anchor && top !== undefined) window.scrollBy({ top: anchor.getBoundingClientRect().top - top, behavior: "instant" });
};

export const referenceControls = (root: HTMLElement, header: HTMLElement): void => {
  const collapse = document.createElement("button");
  collapse.type = "button"; collapse.className = "bn-button reference-collapse";
  collapse.textContent = "Collapse reference"; collapse.setAttribute("aria-expanded", "true");
  collapse.addEventListener("click", () => preserveReadingPlace(() => {
    const collapsed = root.classList.toggle("is-collapsed");
    document.body.classList.toggle("reference-collapsed", collapsed);
    collapse.textContent = collapsed ? "Expand reference" : "Collapse reference";
    collapse.setAttribute("aria-expanded", String(!collapsed));
  }));
  header.append(collapse);
  const handle = document.createElement("div");
  handle.className = "reference-resize"; handle.tabIndex = 0;
  handle.setAttribute("role", "separator"); handle.setAttribute("aria-label", "Resize reference");
  handle.setAttribute("aria-orientation", "vertical");
  const apply = (width: number): void => {
    const max = Math.max(280, Math.min(640, innerWidth * 0.5));
    width = Math.round(Math.max(280, Math.min(max, width)));
    document.body.style.setProperty("--bn-reference-width", `${width}px`);
    handle.setAttribute("aria-valuemin", "280"); handle.setAttribute("aria-valuemax", String(Math.round(max)));
    handle.setAttribute("aria-valuenow", String(width));
  };
  let preferredWidth = 416;
  const save = (): void => {
    preferredWidth = Number(handle.getAttribute("aria-valuenow")) || 416;
    try { localStorage.setItem("bn.reference-width", String(preferredWidth)); }
    catch { /* Layout remains usable without storage. */ }
  };
  let saved = 416;
  try { saved = Number(localStorage.getItem("bn.reference-width")) || saved; } catch { /* Use the default width. */ }
  preferredWidth = saved;
  apply(saved);
  handle.addEventListener("pointerdown", (event) => {
    if (event.button !== 0) return;
    handle.setPointerCapture(event.pointerId); handle.focus({ preventScroll: true }); event.preventDefault();
  });
  handle.addEventListener("pointermove", (event) => {
    if (handle.hasPointerCapture(event.pointerId)) preserveReadingPlace(() => apply(innerWidth - event.clientX));
  });
  handle.addEventListener("pointerup", save);
  handle.addEventListener("keydown", (event) => {
    if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
    event.preventDefault();
    const current = Number(handle.getAttribute("aria-valuenow"));
    preserveReadingPlace(() => apply(event.key === "Home" ? 280 : event.key === "End" ? 640 : current + (event.key === "ArrowLeft" ? 24 : -24)));
    save();
  });
  window.addEventListener("resize", () => apply(preferredWidth));
  root.prepend(handle);
};
