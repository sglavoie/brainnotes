import { queryAllElements } from "./dom";

export const THEME_STORAGE_KEY = "bn.theme";
export const THEME_EVENT = "bn:themechange";
const DARK_MEDIA_QUERY = "(prefers-color-scheme: dark)";

export type ThemePreference = "system" | "light" | "dark";
export type EffectiveTheme = "light" | "dark";

export type ThemeChangeDetail = {
  preference: ThemePreference;
  effective: EffectiveTheme;
};

type StorageLike = Pick<Storage, "getItem" | "setItem" | "removeItem">;

export type InitThemeOptions = {
  document?: Document;
  storage?: StorageLike | null;
};

export const isExplicitPreference = (value: unknown): value is EffectiveTheme =>
  value === "light" || value === "dark";

export const readStoredPreference = (
  storage: StorageLike | null | undefined
): ThemePreference => {
  if (!storage) {
    return "system";
  }
  let stored: string | null;
  try {
    stored = storage.getItem(THEME_STORAGE_KEY);
  } catch {
    return "system";
  }
  return isExplicitPreference(stored) ? stored : "system";
};

export const resolveEffectiveTheme = (
  preference: ThemePreference,
  prefersDark: boolean
): EffectiveTheme => {
  if (isExplicitPreference(preference)) {
    return preference;
  }
  return prefersDark ? "dark" : "light";
};

export const applyThemePreference = (
  root: HTMLElement,
  preference: ThemePreference
): void => {
  if (isExplicitPreference(preference)) {
    root.dataset.theme = preference;
  } else {
    delete root.dataset.theme;
  }
};

export const persistThemePreference = (
  storage: StorageLike | null | undefined,
  preference: ThemePreference
): void => {
  if (!storage) {
    return;
  }
  try {
    if (isExplicitPreference(preference)) {
      storage.setItem(THEME_STORAGE_KEY, preference);
    } else {
      storage.removeItem(THEME_STORAGE_KEY);
    }
  } catch {
    // Storage may be unavailable (private mode); the preference still applies for this page.
  }
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

export const initTheme = (options: InitThemeOptions = {}): void => {
  const doc = options.document ?? (typeof document !== "undefined" ? document : null);
  if (!doc) {
    return;
  }
  const root = doc.documentElement;
  if (!root) {
    return;
  }
  const view = doc.defaultView;
  const storage =
    options.storage !== undefined ? options.storage : safeLocalStorage(view);

  const darkQuery =
    view && typeof view.matchMedia === "function"
      ? view.matchMedia(DARK_MEDIA_QUERY)
      : null;
  const prefersDark = (): boolean => Boolean(darkQuery && darkQuery.matches);

  const controls = queryAllElements(doc, "[data-theme-control]", HTMLElement);

  const syncControls = (preference: ThemePreference): void => {
    controls.forEach((control) => {
      queryAllElements(control, "[data-theme-option]", HTMLInputElement).forEach(
        (option) => {
          option.checked = option.value === preference;
        }
      );
    });
  };

  const apply = (preference: ThemePreference): void => {
    applyThemePreference(root, preference);
    syncControls(preference);
    const detail: ThemeChangeDetail = {
      preference,
      effective: resolveEffectiveTheme(preference, prefersDark()),
    };
    doc.dispatchEvent(new CustomEvent<ThemeChangeDetail>(THEME_EVENT, { detail }));
  };

  // themeBoot already set the initial dataset.theme to avoid a flash; align the
  // control state and announce the resolved theme for downstream listeners.
  apply(readStoredPreference(storage));

  controls.forEach((control) => {
    control.addEventListener("change", (event) => {
      const target = event.target;
      if (
        !(target instanceof HTMLInputElement) ||
        !target.hasAttribute("data-theme-option")
      ) {
        return;
      }
      const preference: ThemePreference = isExplicitPreference(target.value)
        ? target.value
        : "system";
      persistThemePreference(storage, preference);
      apply(preference);
    });
  });

  if (view) {
    view.addEventListener("storage", (event) => {
      if (event.key !== null && event.key !== THEME_STORAGE_KEY) {
        return;
      }
      apply(readStoredPreference(storage));
    });
  }

  if (darkQuery) {
    const onSystemChange = (): void => {
      // Only System preference tracks the OS; explicit overrides are unaffected.
      if (readStoredPreference(storage) === "system") {
        apply("system");
      }
    };
    if (typeof darkQuery.addEventListener === "function") {
      darkQuery.addEventListener("change", onSystemChange);
    } else if (typeof darkQuery.addListener === "function") {
      darkQuery.addListener(onSystemChange);
    }
  }
};
