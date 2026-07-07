// @vitest-environment jsdom

import { afterEach, describe, expect, it } from "vitest";

import {
  applyThemePreference,
  initTheme,
  isExplicitPreference,
  persistThemePreference,
  readStoredPreference,
  resolveEffectiveTheme,
  THEME_EVENT,
  THEME_STORAGE_KEY,
  type ThemeChangeDetail,
} from "./theme";

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

type FakeMediaQuery = {
  matches: boolean;
  media: string;
  onchange: null;
  addEventListener: (type: string, cb: () => void) => void;
  removeEventListener: (type: string, cb: () => void) => void;
  addListener: (cb: () => void) => void;
  removeListener: (cb: () => void) => void;
  dispatchEvent: () => boolean;
  fire: () => void;
  set: (value: boolean) => void;
};

const makeMediaQuery = (matches: boolean): FakeMediaQuery => {
  const listeners = new Set<() => void>();
  const mql: FakeMediaQuery = {
    matches,
    media: "(prefers-color-scheme: dark)",
    onchange: null,
    addEventListener: (_type, cb) => {
      listeners.add(cb);
    },
    removeEventListener: (_type, cb) => {
      listeners.delete(cb);
    },
    addListener: (cb) => {
      listeners.add(cb);
    },
    removeListener: (cb) => {
      listeners.delete(cb);
    },
    dispatchEvent: () => true,
    fire: () => {
      listeners.forEach((cb) => cb());
    },
    set: (value) => {
      mql.matches = value;
    },
  };
  return mql;
};

const installMatchMedia = (mql: FakeMediaQuery): void => {
  window.matchMedia = ((_query: string): MediaQueryList =>
    mql as unknown as MediaQueryList) as typeof window.matchMedia;
};

const controlFixture = (): string => `
  <fieldset data-theme-control>
    <input type="radio" name="bn-theme" value="system" data-theme-option checked />
    <input type="radio" name="bn-theme" value="light" data-theme-option />
    <input type="radio" name="bn-theme" value="dark" data-theme-option />
  </fieldset>
`;

const collectThemeEvents = (): ThemeChangeDetail[] => {
  const details: ThemeChangeDetail[] = [];
  document.addEventListener(THEME_EVENT, (event) => {
    details.push((event as CustomEvent<ThemeChangeDetail>).detail);
  });
  return details;
};

const optionByValue = (value: string): HTMLInputElement => {
  const input = document.querySelector<HTMLInputElement>(
    `[data-theme-option][value="${value}"]`
  );
  if (!input) {
    throw new Error(`missing option ${value}`);
  }
  return input;
};

afterEach(() => {
  document.body.innerHTML = "";
  delete document.documentElement.dataset.theme;
});

describe("theme preference helpers", () => {
  it("recognizes only light and dark as explicit preferences", () => {
    expect(isExplicitPreference("light")).toBe(true);
    expect(isExplicitPreference("dark")).toBe(true);
    expect(isExplicitPreference("system")).toBe(false);
    expect(isExplicitPreference("")).toBe(false);
    expect(isExplicitPreference(null)).toBe(false);
  });

  it("reads a valid stored preference and falls back to system otherwise", () => {
    expect(readStoredPreference(makeStorage({ [THEME_STORAGE_KEY]: "dark" }))).toBe("dark");
    expect(readStoredPreference(makeStorage({ [THEME_STORAGE_KEY]: "light" }))).toBe("light");
    expect(readStoredPreference(makeStorage({ [THEME_STORAGE_KEY]: "bogus" }))).toBe("system");
    expect(readStoredPreference(makeStorage())).toBe("system");
    expect(readStoredPreference(null)).toBe("system");
  });

  it("treats throwing storage as system", () => {
    const throwing = {
      getItem: () => {
        throw new Error("blocked");
      },
      setItem: () => undefined,
      removeItem: () => undefined,
    };
    expect(readStoredPreference(throwing)).toBe("system");
  });

  it("resolves the effective theme from preference and OS setting", () => {
    expect(resolveEffectiveTheme("light", true)).toBe("light");
    expect(resolveEffectiveTheme("dark", false)).toBe("dark");
    expect(resolveEffectiveTheme("system", true)).toBe("dark");
    expect(resolveEffectiveTheme("system", false)).toBe("light");
  });

  it("applies explicit preferences to the dataset and clears it for system", () => {
    const root = document.documentElement;
    applyThemePreference(root, "dark");
    expect(root.dataset.theme).toBe("dark");
    applyThemePreference(root, "light");
    expect(root.dataset.theme).toBe("light");
    applyThemePreference(root, "system");
    expect(root.dataset.theme).toBeUndefined();
  });

  it("persists explicit preferences and removes storage for system", () => {
    const storage = makeStorage();
    persistThemePreference(storage, "dark");
    expect(storage.map.get(THEME_STORAGE_KEY)).toBe("dark");
    persistThemePreference(storage, "system");
    expect(storage.map.has(THEME_STORAGE_KEY)).toBe(false);
  });
});

describe("initTheme integration", () => {
  it("syncs the control and announces the theme on init", () => {
    document.body.innerHTML = controlFixture();
    installMatchMedia(makeMediaQuery(false));
    const events = collectThemeEvents();

    initTheme({ storage: makeStorage({ [THEME_STORAGE_KEY]: "dark" }) });

    expect(document.documentElement.dataset.theme).toBe("dark");
    expect(optionByValue("dark").checked).toBe(true);
    expect(optionByValue("system").checked).toBe(false);
    expect(events).toEqual([{ preference: "dark", effective: "dark" }]);
  });

  it("ignores invalid stored values and stays on system", () => {
    document.body.innerHTML = controlFixture();
    installMatchMedia(makeMediaQuery(true));

    initTheme({ storage: makeStorage({ [THEME_STORAGE_KEY]: "purple" }) });

    expect(document.documentElement.dataset.theme).toBeUndefined();
    expect(optionByValue("system").checked).toBe(true);
  });

  it("stores an explicit preference when a control is selected", () => {
    document.body.innerHTML = controlFixture();
    installMatchMedia(makeMediaQuery(false));
    const storage = makeStorage();
    const events = collectThemeEvents();

    initTheme({ storage });

    const light = optionByValue("light");
    light.checked = true;
    light.dispatchEvent(new Event("change", { bubbles: true }));

    expect(storage.map.get(THEME_STORAGE_KEY)).toBe("light");
    expect(document.documentElement.dataset.theme).toBe("light");
    expect(events.at(-1)).toEqual({ preference: "light", effective: "light" });
  });

  it("clears storage when System is selected", () => {
    document.body.innerHTML = controlFixture();
    installMatchMedia(makeMediaQuery(true));
    const storage = makeStorage({ [THEME_STORAGE_KEY]: "dark" });

    initTheme({ storage });

    const system = optionByValue("system");
    system.checked = true;
    system.dispatchEvent(new Event("change", { bubbles: true }));

    expect(storage.map.has(THEME_STORAGE_KEY)).toBe(false);
    expect(document.documentElement.dataset.theme).toBeUndefined();
  });

  it("reacts to storage events from other tabs", () => {
    document.body.innerHTML = controlFixture();
    installMatchMedia(makeMediaQuery(false));
    const storage = makeStorage();

    initTheme({ storage });

    storage.map.set(THEME_STORAGE_KEY, "dark");
    window.dispatchEvent(
      new StorageEvent("storage", { key: THEME_STORAGE_KEY, newValue: "dark" })
    );

    expect(document.documentElement.dataset.theme).toBe("dark");
    expect(optionByValue("dark").checked).toBe(true);
  });

  it("re-announces the effective theme when the OS preference changes under System", () => {
    document.body.innerHTML = controlFixture();
    const media = makeMediaQuery(false);
    installMatchMedia(media);
    const events = collectThemeEvents();

    initTheme({ storage: makeStorage() });
    expect(events.at(-1)).toEqual({ preference: "system", effective: "light" });

    media.set(true);
    media.fire();

    expect(events.at(-1)).toEqual({ preference: "system", effective: "dark" });
    expect(document.documentElement.dataset.theme).toBeUndefined();
  });

  it("does not follow the OS when an explicit preference is stored", () => {
    document.body.innerHTML = controlFixture();
    const media = makeMediaQuery(false);
    installMatchMedia(media);
    const events = collectThemeEvents();

    initTheme({ storage: makeStorage({ [THEME_STORAGE_KEY]: "light" }) });
    const countAfterInit = events.length;

    media.set(true);
    media.fire();

    expect(events.length).toBe(countAfterInit);
    expect(document.documentElement.dataset.theme).toBe("light");
  });
});
