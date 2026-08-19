import { requestRelated } from "./related-api";
import { requestRelatedItem, trackRelatedView, type RelatedBeacon } from "./related-actions";
import {
  RELATED_MAX_LIMIT,
  createRelatedModel,
  nextRelatedLimit,
  relatedModeDefinitions,
  validateRelatedLaterDate,
  type RelatedModel,
} from "./related-model";
import {
  createRelatedView,
  renderRelatedFeedback,
  renderRelatedResponse,
  selectRelatedModeView,
  setRelatedCardBusy,
  setRelatedFeedbackBusy,
  setRelatedModeBusy,
  type RelatedView,
} from "./related-render";
import { setStatusText, type StatusKind } from "./status";
import type { RelatedFetch, RelatedItemAction, RelatedMode } from "./related-types";

export type RelatedControllerDeps = {
  fetch?: RelatedFetch;
  sendBeacon?: RelatedBeacon;
};

export type RelatedConfig = {
  source: string;
  endpoint: string;
  itemEndpoint: string;
  csrfToken: string;
};

export const readRelatedConfig = (root: HTMLElement): RelatedConfig => ({
  source: (root.getAttribute("data-related-source-key") || "").trim(),
  endpoint: root.getAttribute("data-related-endpoint") || "/api/related",
  itemEndpoint: root.getAttribute("data-related-item-endpoint") || "/api/related/item",
  csrfToken: root.getAttribute("data-related-csrf") || "",
});

const errorMessage = (error: unknown): string =>
  error instanceof Error && error.message !== "" ? error.message : "Related notes could not be loaded.";

const statusForMode = (model: RelatedModel, mode: RelatedMode): [string, StatusKind] => {
  const state = model.modes[mode];
  if (state.loading) {
    return [`Loading ${mode} related notes…`, "is-loading"];
  }
  if (state.error !== "") {
    return [state.error, "is-error"];
  }
  if (state.response) {
    if (state.response.results.length === 0) {
      return ["No related notes are available in this mode.", "is-empty"];
    }
    const count = state.response.results.length;
    return [`Showing ${count} related ${count === 1 ? "note" : "notes"}.`, "is-success"];
  }
  return ["Select a mode to load related notes.", ""];
};

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

const cardForCandidate = (view: RelatedView, mode: RelatedMode, candidate: string): HTMLElement | null => {
  for (const card of view.results[mode].querySelectorAll<HTMLElement>("[data-related-card]")) {
    if (card.getAttribute("data-related-card") === candidate) {
      return card;
    }
  }
  return null;
};

export const createRelatedController = (root: HTMLElement, deps: RelatedControllerDeps = {}): void => {
  const config = readRelatedConfig(root);
  const model = createRelatedModel();
  const view = createRelatedView(root);
  const request = deps.fetch || fetch;
  const fetcher: RelatedFetch = (input, init) => request(input, init);
  const beacon =
    deps.sendBeacon ||
    (typeof navigator !== "undefined" && typeof navigator.sendBeacon === "function"
      ? navigator.sendBeacon.bind(navigator)
      : undefined);
  const requestIDs: Record<RelatedMode, number> = { balanced: 0, rediscover: 0, connect: 0 };
  const loadPromises: Record<RelatedMode, Promise<boolean> | null> = {
    balanced: null,
    rediscover: null,
    connect: null,
  };

  const setStatus = (message: string, kind: StatusKind = ""): void => {
    setStatusText(view.status, message, kind);
  };

  const showStatus = (mode: RelatedMode): void => {
    if (model.activeMode !== mode) {
      return;
    }
    const [message, kind] = statusForMode(model, mode);
    setStatus(message, kind);
  };

  const load = (mode: RelatedMode, requestedLimit: number): Promise<boolean> => {
    const state = model.modes[mode];
    if (state.loading) {
      return Promise.resolve(false);
    }
    state.loading = true;
    state.error = "";
    const requestID = requestIDs[mode] + 1;
    requestIDs[mode] = requestID;
    setRelatedModeBusy(view, mode, true);
    showStatus(mode);
    const loadPromise = (async (): Promise<boolean> => {
      let succeeded = false;
      try {
        const response = await requestRelated(fetcher, config.endpoint, config.source, mode, requestedLimit);
        if (requestIDs[mode] !== requestID) {
          return false;
        }
        state.limit = requestedLimit;
        state.loaded = true;
        state.stale = false;
        state.response = response;
        model.feedback = response.feedback;
        model.serverDate = response.serverDate;
        renderRelatedResponse(view, response, requestedLimit);
        renderRelatedFeedback(view, model.feedback);
        succeeded = true;
      } catch (error) {
        if (requestIDs[mode] !== requestID) {
          return false;
        }
        state.error = errorMessage(error);
      } finally {
        if (requestIDs[mode] === requestID) {
          state.loading = false;
          setRelatedModeBusy(view, mode, false);
          showStatus(mode);
        }
      }
      return succeeded;
    })();
    loadPromises[mode] = loadPromise;
    const clearLoadPromise = (): void => {
      if (loadPromises[mode] === loadPromise) {
        loadPromises[mode] = null;
      }
    };
    void loadPromise.then(clearLoadPromise, clearLoadPromise);
    return loadPromise;
  };

  const refreshAfterMutation = async (mode: RelatedMode): Promise<boolean> => {
    const pendingLoad = loadPromises[mode];
    if (pendingLoad) {
      await pendingLoad;
    }
    return load(mode, model.modes[mode].limit);
  };

  const selectMode = (mode: RelatedMode): void => {
    model.activeMode = mode;
    selectRelatedModeView(view, mode);
    showStatus(mode);
    const state = model.modes[mode];
    if ((!state.loaded || state.stale) && !state.loading) {
      void load(mode, state.limit);
    }
  };

  const markInactiveModesStale = (): void => {
    for (const definition of relatedModeDefinitions) {
      if (definition.mode !== model.activeMode) {
        model.modes[definition.mode].stale = true;
      }
    }
  };

  const focusAfterMutation = (
    candidate: string,
    nextCandidate: string,
    previousCandidate: string,
    fromDrawer: boolean
  ): void => {
    const preferred = fromDrawer ? [candidate] : [candidate, nextCandidate, previousCandidate];
    for (const key of preferred) {
      if (key === "") {
        continue;
      }
      const card = cardForCandidate(view, model.activeMode, key);
      if (card) {
        card.focus({ preventScroll: true });
        return;
      }
    }
    (fromDrawer ? view.feedbackSummary : view.tabs[model.activeMode]).focus({ preventScroll: true });
  };

  const runMutation = async (
    action: RelatedItemAction,
    candidate: string,
    until: string,
    origin: HTMLElement
  ): Promise<void> => {
    if (config.source === "" || config.csrfToken === "" || candidate === "") {
      setStatus("Related action unavailable.", "is-error");
      return;
    }
    if (action === "later") {
      const validationError = validateRelatedLaterDate(model.serverDate, until);
      if (validationError) {
        setStatus(validationError, "is-error");
        return;
      }
    }
    const card = origin.closest<HTMLElement>("[data-related-card]");
    const drawerRow = origin.closest<HTMLElement>("[data-related-feedback-row]");
    const cards = Array.from(view.results[model.activeMode].querySelectorAll<HTMLElement>("[data-related-card]"));
    const index = cards.findIndex((item) => item.getAttribute("data-related-card") === candidate);
    const nextCandidate = index >= 0 ? cards[index + 1]?.getAttribute("data-related-card") || "" : "";
    const previousCandidate = index > 0 ? cards[index - 1]?.getAttribute("data-related-card") || "" : "";
    if (card) {
      setRelatedCardBusy(card, true);
    }
    if (drawerRow) {
      setRelatedFeedbackBusy(drawerRow, true);
    }
    setStatus(action === "open_editor" ? "Opening editor…" : "Saving feedback…", "is-loading");

    try {
      const result = await requestRelatedItem(fetcher, config.itemEndpoint, {
        source: config.source,
        candidate,
        action,
        token: config.csrfToken,
        ...(until ? { until } : {}),
      });
      if (action === "open_editor") {
        if (result.warning) {
          setStatus(`Editor opened. ${result.warning}`, "is-warning");
        } else {
          setStatus("Opened in editor and recorded in recall history.", "is-success");
        }
        return;
      }

      markInactiveModesStale();
      const activeMode = model.activeMode;
      const activeState = model.modes[activeMode];
      const refreshed = await refreshAfterMutation(activeMode);
      if (!refreshed) {
        setStatus(
          `Feedback was saved, but related notes could not be refreshed: ${
            activeState.error || "refresh failed"
          }`,
          "is-warning"
        );
        return;
      }
      const outcome =
        action === "dismissed"
          ? "Dismissed note."
          : action === "later"
            ? `Hidden until ${until}.`
            : action === "clear"
              ? "Cleared feedback."
              : "Marked note as useful.";
      setStatus(outcome, "is-success");
      focusAfterMutation(candidate, nextCandidate, previousCandidate, drawerRow !== null);
    } catch (error) {
      setStatus(errorMessage(error), "is-error");
    } finally {
      if (card?.isConnected) {
        setRelatedCardBusy(card, false);
      }
      if (drawerRow?.isConnected) {
        setRelatedFeedbackBusy(drawerRow, false);
      }
    }
  };

  for (const definition of relatedModeDefinitions) {
    const { mode } = definition;
    view.tabs[mode].addEventListener("click", () => selectMode(mode));
    view.tabs[mode].addEventListener("keydown", (event) => {
      const currentIndex = relatedModeDefinitions.findIndex((item) => item.mode === mode);
      let targetIndex = currentIndex;
      switch (event.key) {
        case "ArrowLeft":
          targetIndex = (currentIndex - 1 + relatedModeDefinitions.length) % relatedModeDefinitions.length;
          break;
        case "ArrowRight":
          targetIndex = (currentIndex + 1) % relatedModeDefinitions.length;
          break;
        case "Home":
          targetIndex = 0;
          break;
        case "End":
          targetIndex = relatedModeDefinitions.length - 1;
          break;
        default:
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
      void load(mode, nextRelatedLimit(state.limit));
    });
  }

  root.addEventListener("click", (event) => {
    const target = event.target instanceof Element ? event.target : null;
    const link = target?.closest<HTMLAnchorElement>("[data-related-view-candidate]");
    if (link) {
      const candidate = link.getAttribute("data-related-view-candidate") || "";
      if (candidate && config.source && config.csrfToken) {
        trackRelatedView(fetcher, beacon, config.itemEndpoint, {
          source: config.source,
          candidate,
          token: config.csrfToken,
        });
      }
      return;
    }
    const button = target?.closest<HTMLButtonElement>("[data-related-action]");
    if (!button || button.disabled) {
      return;
    }
    let action = actionFromValue(button.getAttribute("data-related-action") || "");
    if (!action) {
      return;
    }
    if (action === "useful" && button.getAttribute("aria-pressed") === "true") {
      action = "clear";
    }
    void runMutation(
      action,
      button.getAttribute("data-related-candidate") || "",
      button.getAttribute("data-related-until") || "",
      button
    );
  });

  root.addEventListener("submit", (event) => {
    const form = event.target instanceof HTMLFormElement ? event.target : null;
    if (!form?.matches("[data-related-later-form]")) {
      return;
    }
    event.preventDefault();
    const input = form.querySelector<HTMLInputElement>("[data-related-later-date]");
    void runMutation("later", form.getAttribute("data-related-later-form") || "", input?.value || "", form);
  });

  if (config.source === "") {
    model.modes.balanced.error = "Related notes are unavailable for this page.";
    showStatus("balanced");
    return;
  }
  selectMode("balanced");
};
