import { trackRelatedView, type RelatedBeacon } from "./related-actions";
import { createRelatedLoader, type RelatedLoader } from "./related-loader";
import { createRelatedMutation, type RelatedMutation } from "./related-mutations";
import { RELATED_MAX_LIMIT, nextRelatedLimit, relatedModeDefinitions } from "./related-model";
import { selectRelatedModeView } from "./related-render";
import type { RelatedConfig, RelatedFetch, RelatedItemAction, RelatedMode } from "./related-types";

export type RelatedControllerDeps = {
  fetch?: RelatedFetch;
  sendBeacon?: RelatedBeacon;
};

export type { RelatedConfig } from "./related-types";

export const readRelatedConfig = (root: HTMLElement): RelatedConfig => ({
  source: (root.getAttribute("data-related-source-key") || "").trim(),
  endpoint: root.getAttribute("data-related-endpoint") || "/api/related",
  itemEndpoint: root.getAttribute("data-related-item-endpoint") || "/api/related/item",
  csrfToken: root.getAttribute("data-related-csrf") || "",
});

const actionFromValue = (value: string): RelatedItemAction | null => {
  switch (value) {
    case "open_editor":
    case "useful":
    case "dismissed":
    case "later":
    case "clear":
      return value;
    default:
      return null;
  }
};

const nextModeIndex = (key: string, currentIndex: number): number | null => {
  const count = relatedModeDefinitions.length;
  switch (key) {
    case "ArrowLeft":
      return (currentIndex - 1 + count) % count;
    case "ArrowRight":
      return (currentIndex + 1) % count;
    case "Home":
      return 0;
    case "End":
      return count - 1;
    default:
      return null;
  }
};

const wireModeTabs = (
  loader: RelatedLoader,
  selectMode: (mode: RelatedMode) => void
): void => {
  const { model, view } = loader;
  for (const definition of relatedModeDefinitions) {
    const { mode } = definition;
    view.tabs[mode].addEventListener("click", () => selectMode(mode));
    view.tabs[mode].addEventListener("keydown", (event) => {
      const currentIndex = relatedModeDefinitions.findIndex((item) => item.mode === mode);
      const targetIndex = nextModeIndex(event.key, currentIndex);
      if (targetIndex === null) {
        return;
      }
      event.preventDefault();
      const targetMode = relatedModeDefinitions[targetIndex].mode;
      view.tabs[targetMode].focus();
      selectMode(targetMode);
    });
    view.moreButtons[mode].addEventListener("click", () => {
      const state = model.modes[mode];
      if (state.loading || state.limit >= RELATED_MAX_LIMIT || !state.response?.hasMore) {
        return;
      }
      void loader.load(mode, nextRelatedLimit(state.limit));
    });
  }
};

/**
 * trackViewClick reports whether the click landed on a candidate link, not
 * whether it was tracked: a link click leaves the page either way, so the
 * action handling below it must not also run.
 */
const trackViewClick = (
  target: Element | null,
  config: RelatedConfig,
  fetcher: RelatedFetch,
  beacon: RelatedBeacon | undefined
): boolean => {
  const link = target?.closest<HTMLAnchorElement>("[data-related-view-candidate]");
  if (!link) {
    return false;
  }
  const candidate = link.getAttribute("data-related-view-candidate") || "";
  if (candidate && config.source && config.csrfToken) {
    trackRelatedView(fetcher, beacon, config.itemEndpoint, {
      source: config.source,
      candidate,
      token: config.csrfToken,
    });
  }
  return true;
};

type RelatedActionClick = {
  action: RelatedItemAction;
  candidate: string;
  until: string;
  button: HTMLButtonElement;
};

const actionClickFromTarget = (target: Element | null): RelatedActionClick | null => {
  const button = target?.closest<HTMLButtonElement>("[data-related-action]");
  if (!button || button.disabled) {
    return null;
  }
  const action = actionFromValue(button.getAttribute("data-related-action") || "");
  if (!action) {
    return null;
  }
  const pressed = action === "useful" && button.getAttribute("aria-pressed") === "true";
  return {
    action: pressed ? "clear" : action,
    candidate: button.getAttribute("data-related-candidate") || "",
    until: button.getAttribute("data-related-until") || "",
    button,
  };
};

const wireRootEvents = (
  root: HTMLElement,
  config: RelatedConfig,
  fetcher: RelatedFetch,
  beacon: RelatedBeacon | undefined,
  runMutation: RelatedMutation
): void => {
  root.addEventListener("click", (event) => {
    const target = event.target instanceof Element ? event.target : null;
    if (trackViewClick(target, config, fetcher, beacon)) {
      return;
    }
    const click = actionClickFromTarget(target);
    if (click) {
      void runMutation(click.action, click.candidate, click.until, click.button);
    }
  });

  root.addEventListener("submit", (event) => {
    const form = event.target instanceof HTMLFormElement ? event.target : null;
    if (!form?.matches("[data-related-later-form]")) {
      return;
    }
    event.preventDefault();
    const input = form.querySelector<HTMLInputElement>("[data-related-later-date]");
    void runMutation(
      "later",
      form.getAttribute("data-related-later-form") || "",
      input?.value || "",
      form
    );
  });
};

const defaultBeacon = (deps: RelatedControllerDeps): RelatedBeacon | undefined =>
  deps.sendBeacon ||
  (typeof navigator !== "undefined" && typeof navigator.sendBeacon === "function"
    ? navigator.sendBeacon.bind(navigator)
    : undefined);

export const createRelatedController = (
  root: HTMLElement,
  deps: RelatedControllerDeps = {}
): void => {
  const config = readRelatedConfig(root);
  const request = deps.fetch || fetch;
  const fetcher: RelatedFetch = (input, init) => request(input, init);
  const loader = createRelatedLoader(root, config, fetcher);
  const { model, view } = loader;

  const selectMode = (mode: RelatedMode): void => {
    model.activeMode = mode;
    selectRelatedModeView(view, mode);
    loader.showStatus(mode);
    const state = model.modes[mode];
    if ((!state.loaded || state.stale) && !state.loading) {
      void loader.load(mode, state.limit);
    }
  };

  wireModeTabs(loader, selectMode);
  wireRootEvents(root, config, fetcher, defaultBeacon(deps), createRelatedMutation(loader, config, fetcher));

  if (config.source === "") {
    model.modes.balanced.error = "Related notes are unavailable for this page.";
    loader.showStatus("balanced");
    return;
  }
  selectMode("balanced");
};
