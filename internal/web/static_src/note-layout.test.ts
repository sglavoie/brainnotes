// @vitest-environment jsdom

import { afterEach, describe, expect, it } from "vitest";

import {
  clampMetaSidebarWidth,
  initNoteLayout,
  NOTE_META_VISIBLE_STORAGE_KEY,
  NOTE_META_WIDTH_STORAGE_KEY,
  NOTE_TOC_WIDTH_STORAGE_KEY,
  persistMetaSidebarVisible,
  persistSidebarWidth,
  readStoredMetaSidebarVisible,
  readStoredSidebarWidth,
} from "./note-layout";

type FakeStorage = {
  getItem: (key: string) => string | null;
  setItem: (key: string, value: string) => void;
  removeItem: (key: string) => void;
  map: Map<string, string>;
};

const makeStorage = (initial: Record<string, string> = {}): FakeStorage => {
  const map = new Map<string, string>(Object.entries(initial));
  return {
    map,
    getItem: (key) => (map.has(key) ? (map.get(key) as string) : null),
    setItem: (key, value) => {
      map.set(key, value);
    },
    removeItem: (key) => {
      map.delete(key);
    },
  };
};

const setViewportWidth = (width: number): void => {
  Object.defineProperty(window, "innerWidth", { configurable: true, value: width });
};

const layoutFixture = (withTOC = true): string => `
  <div class="note-layout-toolbar">
    <button type="button" class="note-sidebar-toggle" data-note-sidebar-toggle aria-controls="note-metadata-sidebar" aria-expanded="true" aria-label="Hide sidebar" title="Hide sidebar">
      <svg class="note-sidebar-toggle-icon" viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" focusable="false">
        <rect x="0.75" y="2.75" width="14.5" height="10.5" rx="1.5" fill="none" stroke="currentColor" stroke-width="1.5"></rect>
        <rect class="note-sidebar-toggle-panel" x="0.75" y="2.75" width="5" height="10.5" fill="currentColor"></rect>
      </svg>
    </button>
  </div>
  <div class="note-layout${withTOC ? " note-layout-with-toc" : ""}" data-note-layout>
    <aside id="note-metadata-sidebar" class="note-meta-sidebar">
      <div data-note-meta-resize role="separator" tabindex="0"></div>
    </aside>
    ${withTOC ? '<aside class="note-toc-sidebar"></aside>' : ""}
    <div class="note-content"></div>
  </div>
`;

const mockWidth = (element: Element, width: number, left = 0): void => {
  Object.defineProperty(element, "getBoundingClientRect", {
    configurable: true,
    value: () => ({
      bottom: 0,
      height: 0,
      left,
      right: left + width,
      top: 0,
      width,
      x: left,
      y: 0,
      toJSON: () => ({}),
    }),
  });
};

afterEach(() => {
  document.body.innerHTML = "";
  delete document.documentElement.dataset.noteMetaSidebar;
  document.documentElement.style.removeProperty("--bn-note-toc-sidebar-width");
  setViewportWidth(1024);
});

describe("note layout storage helpers", () => {
  it("uses visible and valid-width defaults, and safely persists preferences", () => {
    const storage = makeStorage({
      [NOTE_META_VISIBLE_STORAGE_KEY]: "hidden",
      [NOTE_META_WIDTH_STORAGE_KEY]: "384",
    });

    expect(readStoredMetaSidebarVisible(storage)).toBe(false);
    expect(readStoredSidebarWidth(storage, NOTE_META_WIDTH_STORAGE_KEY)).toBe(384);
    expect(readStoredSidebarWidth(makeStorage({ [NOTE_META_WIDTH_STORAGE_KEY]: "bad" }), NOTE_META_WIDTH_STORAGE_KEY)).toBeNull();
    expect(readStoredSidebarWidth(makeStorage({ [NOTE_META_WIDTH_STORAGE_KEY]: "200" }), NOTE_META_WIDTH_STORAGE_KEY)).toBeNull();

    persistMetaSidebarVisible(storage, true);
    persistSidebarWidth(storage, NOTE_META_WIDTH_STORAGE_KEY, 401.6);

    expect(storage.map.has(NOTE_META_VISIBLE_STORAGE_KEY)).toBe(false);
    expect(storage.map.get(NOTE_META_WIDTH_STORAGE_KEY)).toBe("402");
  });

  it("clamps metadata width to the selected half-viewport maximum and room for the other columns", () => {
    expect(clampMetaSidebarWidth(100, 1200, false)).toBe(216);
    expect(clampMetaSidebarWidth(1000, 1200, false)).toBe(600);
    expect(clampMetaSidebarWidth(1000, 961, true)).toBe(417);
  });
});

describe("initNoteLayout", () => {
  it("restores saved visibility and width, updating the toggle semantics", () => {
    document.body.innerHTML = layoutFixture();
    const storage = makeStorage({
      [NOTE_META_VISIBLE_STORAGE_KEY]: "hidden",
      [NOTE_META_WIDTH_STORAGE_KEY]: "384",
    });

    initNoteLayout({ storage });

    const layout = document.querySelector<HTMLElement>("[data-note-layout]");
    const metadata = document.querySelector<HTMLElement>(".note-meta-sidebar");
    const toggle = document.querySelector<HTMLButtonElement>("[data-note-sidebar-toggle]");
    expect(layout?.classList.contains("is-metadata-hidden")).toBe(true);
    expect(layout?.style.getPropertyValue("--bn-note-meta-width")).toBe("384px");
    expect(metadata?.hidden).toBe(true);
    expect(toggle?.getAttribute("aria-expanded")).toBe("false");
    expect(toggle?.getAttribute("aria-label")).toBe("Show sidebar");
    expect(toggle?.getAttribute("title")).toBe("Show sidebar");
    expect(toggle?.querySelector(".note-sidebar-toggle-icon")).not.toBeNull();

    toggle?.click();

    expect(metadata?.hidden).toBe(false);
    expect(toggle?.getAttribute("aria-expanded")).toBe("true");
    expect(toggle?.getAttribute("aria-label")).toBe("Hide sidebar");
    expect(toggle?.querySelector(".note-sidebar-toggle-icon")).not.toBeNull();
    expect(storage.map.has(NOTE_META_VISIBLE_STORAGE_KEY)).toBe(false);
  });

  it("freezes the current Contents width before hiding the metadata sidebar", () => {
    document.body.innerHTML = layoutFixture();
    const storage = makeStorage();
    const toc = document.querySelector<HTMLElement>(".note-toc-sidebar");
    expect(toc).not.toBeNull();
    mockWidth(toc!, 284);

    initNoteLayout({ storage });
    document.querySelector<HTMLButtonElement>("[data-note-sidebar-toggle]")?.click();

    const layout = document.querySelector<HTMLElement>("[data-note-layout]");
    expect(layout?.style.getPropertyValue("--bn-note-toc-sidebar-width")).toBe("284px");
    expect(document.documentElement.style.getPropertyValue("--bn-note-toc-sidebar-width")).toBe("284px");
    expect(storage.map.get(NOTE_TOC_WIDTH_STORAGE_KEY)).toBe("284");
  });

  it("resizes with the pointer and keyboard within the desktop bounds", () => {
    document.body.innerHTML = layoutFixture();
    const storage = makeStorage();
    const layout = document.querySelector<HTMLElement>("[data-note-layout]");
    const handle = document.querySelector<HTMLElement>("[data-note-meta-resize]");
    expect(layout).not.toBeNull();
    expect(handle).not.toBeNull();
    mockWidth(layout!, 1024, 40);

    initNoteLayout({ storage });
    handle?.dispatchEvent(new MouseEvent("pointerdown", { bubbles: true, button: 0, clientX: 300 }));
    window.dispatchEvent(new MouseEvent("pointermove", { clientX: 440 }));
    window.dispatchEvent(new MouseEvent("pointerup"));

    expect(layout?.style.getPropertyValue("--bn-note-meta-width")).toBe("400px");
    expect(storage.map.get(NOTE_META_WIDTH_STORAGE_KEY)).toBe("400");

    handle?.dispatchEvent(new KeyboardEvent("keydown", { bubbles: true, key: "ArrowRight" }));

    expect(layout?.style.getPropertyValue("--bn-note-meta-width")).toBe("416px");
    expect(storage.map.get(NOTE_META_WIDTH_STORAGE_KEY)).toBe("416");
    expect(handle?.getAttribute("aria-valuenow")).toBe("416");
  });

  it("keeps the visibility toggle working but does not apply resizing on narrow screens", () => {
    setViewportWidth(960);
    document.body.innerHTML = layoutFixture();
    const storage = makeStorage();

    initNoteLayout({ storage });
    const toggle = document.querySelector<HTMLButtonElement>("[data-note-sidebar-toggle]");
    const layout = document.querySelector<HTMLElement>("[data-note-layout]");
    const handle = document.querySelector<HTMLElement>("[data-note-meta-resize]");
    toggle?.click();
    handle?.dispatchEvent(new MouseEvent("pointerdown", { bubbles: true, button: 0, clientX: 440 }));

    expect(document.querySelector<HTMLElement>(".note-meta-sidebar")?.hidden).toBe(true);
    expect(layout?.style.getPropertyValue("--bn-note-meta-width")).toBe("");
    expect(document.documentElement.classList.contains("is-resizing-note-sidebar")).toBe(false);
  });
});
