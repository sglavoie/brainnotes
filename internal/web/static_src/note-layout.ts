import { queryElement } from "./dom";

export const NOTE_META_VISIBLE_STORAGE_KEY = "bn.note-meta-sidebar.visible";
export const NOTE_META_WIDTH_STORAGE_KEY = "bn.note-meta-sidebar.width";
export const NOTE_TOC_WIDTH_STORAGE_KEY = "bn.note-toc-sidebar.width";

const DESKTOP_BREAKPOINT_PX = 961;
const MIN_META_WIDTH_PX = 13.5 * 16;
const MIN_TOC_WIDTH_PX = 12 * 16;
const MIN_CONTENT_WIDTH_PX = 20 * 16;
const GRID_GAP_PX = 16;
const RESIZE_STEP_PX = 16;
const FAST_RESIZE_STEP_PX = 64;

type StorageLike = Pick<Storage, "getItem" | "setItem" | "removeItem">;

export type InitNoteLayoutOptions = {
  document?: Document;
  storage?: StorageLike | null;
};

const safeLocalStorage = (view: Window | null): StorageLike | null => {
  if (!view) {
    return null;
  }
  try {
    return view.localStorage;
  } catch {
    return null;
  }
};

export const readStoredMetaSidebarVisible = (
  storage: StorageLike | null | undefined
): boolean => {
  if (!storage) {
    return true;
  }
  try {
    return storage.getItem(NOTE_META_VISIBLE_STORAGE_KEY) !== "hidden";
  } catch {
    return true;
  }
};

export const persistMetaSidebarVisible = (
  storage: StorageLike | null | undefined,
  visible: boolean
): void => {
  if (!storage) {
    return;
  }
  try {
    if (visible) {
      storage.removeItem(NOTE_META_VISIBLE_STORAGE_KEY);
    } else {
      storage.setItem(NOTE_META_VISIBLE_STORAGE_KEY, "hidden");
    }
  } catch {
    // Storage may be unavailable; the preference still applies to this page.
  }
};

export const readStoredSidebarWidth = (
  storage: StorageLike | null | undefined,
  key: string
): number | null => {
  if (!storage) {
    return null;
  }
  try {
    const value = Number(storage.getItem(key));
    return Number.isFinite(value) && value >= MIN_META_WIDTH_PX ? value : null;
  } catch {
    return null;
  }
};

export const persistSidebarWidth = (
  storage: StorageLike | null | undefined,
  key: string,
  width: number
): void => {
  if (!storage || !Number.isFinite(width)) {
    return;
  }
  try {
    storage.setItem(key, String(Math.round(width)));
  } catch {
    // Storage may be unavailable; the width still applies to this page.
  }
};

export const clampMetaSidebarWidth = (
  width: number,
  viewportWidth: number,
  hasTableOfContents: boolean
): number => {
  const viewportMaximum = viewportWidth / 2;
  const layoutMaximum = hasTableOfContents
    ? viewportWidth - MIN_TOC_WIDTH_PX - MIN_CONTENT_WIDTH_PX - 2 * GRID_GAP_PX
    : viewportMaximum;
  const maximum = Math.max(MIN_META_WIDTH_PX, Math.min(viewportMaximum, layoutMaximum));
  return Math.round(Math.min(Math.max(width, MIN_META_WIDTH_PX), maximum));
};

const isDesktop = (view: Window): boolean => view.innerWidth >= DESKTOP_BREAKPOINT_PX;

const setStoredTOCWidth = (
  doc: Document,
  layout: HTMLElement,
  toc: HTMLElement | null,
  storage: StorageLike | null
): void => {
  if (!toc) {
    return;
  }
  const width = Math.round(toc.getBoundingClientRect().width);
  if (width < MIN_TOC_WIDTH_PX) {
    return;
  }
  layout.style.setProperty("--bn-note-toc-sidebar-width", `${width}px`);
  doc.documentElement.style.setProperty("--bn-note-toc-sidebar-width", `${width}px`);
  persistSidebarWidth(storage, NOTE_TOC_WIDTH_STORAGE_KEY, width);
};

const bindMetadataResize = (
  doc: Document,
  view: Window,
  layout: HTMLElement,
  metadata: HTMLElement,
  resizeHandle: HTMLElement | null,
  storage: StorageLike | null,
  getPreferredWidth: () => number | null,
  updateWidth: (width: number, persist: boolean) => void
): void => {
  if (!resizeHandle) return;
  resizeHandle.addEventListener("pointerdown", (event) => {
    if (!isDesktop(view) || event.button !== 0) {
      return;
    }
    event.preventDefault();
    doc.documentElement.classList.add("is-resizing-note-sidebar");

    const onPointerMove = (moveEvent: PointerEvent): void => {
      const layoutLeft = layout.getBoundingClientRect().left;
      updateWidth(moveEvent.clientX - layoutLeft, false);
    };
    const finishResize = (): void => {
      doc.documentElement.classList.remove("is-resizing-note-sidebar");
      const width = getPreferredWidth();
      if (width !== null) {
        persistSidebarWidth(storage, NOTE_META_WIDTH_STORAGE_KEY, width);
      }
      view.removeEventListener("pointermove", onPointerMove);
      view.removeEventListener("pointerup", finishResize);
      view.removeEventListener("pointercancel", finishResize);
    };

    view.addEventListener("pointermove", onPointerMove);
    view.addEventListener("pointerup", finishResize, { once: true });
    view.addEventListener("pointercancel", finishResize, { once: true });
  });

  resizeHandle.addEventListener("keydown", (event) => {
    if (!isDesktop(view) || (event.key !== "ArrowLeft" && event.key !== "ArrowRight")) {
      return;
    }
    event.preventDefault();
    const current =
      getPreferredWidth() ?? Math.round(metadata.getBoundingClientRect().width);
    const step = event.shiftKey ? FAST_RESIZE_STEP_PX : RESIZE_STEP_PX;
    updateWidth(current + (event.key === "ArrowRight" ? step : -step), true);
  });
};

export const initNoteLayout = (options: InitNoteLayoutOptions = {}): void => {
  const doc = options.document ?? (typeof document !== "undefined" ? document : null);
  if (!doc) {
    return;
  }
  const view = doc.defaultView;
  if (!view) {
    return;
  }

  const layout = queryElement(doc, "[data-note-layout]", HTMLElement);
  const toggle = queryElement(doc, "[data-note-sidebar-toggle]", HTMLButtonElement);
  const metadata = queryElement(doc, ".note-meta-sidebar", HTMLElement);
  if (!layout || !toggle || !metadata) {
    return;
  }

  const resizeHandle = queryElement(
    metadata,
    "[data-note-meta-resize]",
    HTMLElement
  );
  const toc = queryElement(doc, ".note-toc-sidebar", HTMLElement);
  const storage =
    options.storage !== undefined ? options.storage : safeLocalStorage(view);
  const hasTableOfContents = Boolean(toc);
  let visible = readStoredMetaSidebarVisible(storage);
  let preferredWidth = readStoredSidebarWidth(storage, NOTE_META_WIDTH_STORAGE_KEY);

  const applyWidth = (width: number | null): void => {
    if (!isDesktop(view)) {
      layout.style.removeProperty("--bn-note-meta-width");
      return;
    }
    const renderedWidth = Math.round(metadata.getBoundingClientRect().width);
    const requestedWidth = (width ?? renderedWidth) || MIN_META_WIDTH_PX;
    const clamped = clampMetaSidebarWidth(requestedWidth, view.innerWidth, hasTableOfContents);
    if (width === null) {
      layout.style.removeProperty("--bn-note-meta-width");
    } else {
      layout.style.setProperty("--bn-note-meta-width", `${clamped}px`);
    }
    if (resizeHandle) {
      resizeHandle.setAttribute("aria-valuemin", String(MIN_META_WIDTH_PX));
      resizeHandle.setAttribute(
        "aria-valuemax",
        String(clampMetaSidebarWidth(Number.MAX_SAFE_INTEGER, view.innerWidth, hasTableOfContents))
      );
      resizeHandle.setAttribute("aria-valuenow", String(clamped));
    }
  };

  const applyVisibility = (): void => {
    layout.classList.toggle("is-metadata-hidden", !visible);
    metadata.hidden = !visible && isDesktop(view);
    toggle.setAttribute("aria-expanded", String(visible));
    const label = visible ? "Hide sidebar" : "Show sidebar";
    toggle.setAttribute("aria-label", label);
    toggle.setAttribute("title", label);
    if (visible) {
      delete doc.documentElement.dataset.noteMetaSidebar;
    } else {
      doc.documentElement.dataset.noteMetaSidebar = "hidden";
    }
  };

  const updateWidth = (width: number, persist: boolean): void => {
    preferredWidth = clampMetaSidebarWidth(width, view.innerWidth, hasTableOfContents);
    applyWidth(preferredWidth);
    if (persist) {
      persistSidebarWidth(storage, NOTE_META_WIDTH_STORAGE_KEY, preferredWidth);
    }
  };

  applyWidth(preferredWidth);
  applyVisibility();

  toggle.addEventListener("click", () => {
    if (visible && isDesktop(view)) {
      setStoredTOCWidth(doc, layout, toc, storage);
    }
    visible = !visible;
    persistMetaSidebarVisible(storage, visible);
    applyVisibility();
  });

  bindMetadataResize(doc, view, layout, metadata, resizeHandle, storage,
    () => preferredWidth, updateWidth);

  view.addEventListener("resize", () => {
    applyWidth(preferredWidth);
    applyVisibility();
  });
};
