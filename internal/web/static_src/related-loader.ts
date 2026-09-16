import { requestRelated } from "./related-api";
import { createRelatedModel, type RelatedModel } from "./related-model";
import {
  createRelatedView,
  renderRelatedFeedback,
  renderRelatedResponse,
  setRelatedModeBusy,
  type RelatedView,
} from "./related-render";
import { setStatusText, type StatusKind } from "./status";
import type { RelatedConfig, RelatedFetch, RelatedMode, RelatedResponse } from "./related-types";

export const relatedErrorMessage = (error: unknown): string =>
  error instanceof Error && error.message !== ""
    ? error.message
    : "Related notes could not be loaded.";

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

const applyResponse = (
  model: RelatedModel,
  view: RelatedView,
  mode: RelatedMode,
  limit: number,
  response: RelatedResponse
): void => {
  const state = model.modes[mode];
  state.limit = limit;
  state.loaded = true;
  state.stale = false;
  state.response = response;
  model.feedback = response.feedback;
  model.serverDate = response.serverDate;
  renderRelatedResponse(view, response, limit);
  renderRelatedFeedback(view, model.feedback);
};

/**
 * RelatedLoader owns everything a mode's results depend on: the in-flight
 * request identity, the status line, and the rendering of a response. The
 * mutation side asks it to reload rather than reaching into the model, so there
 * is one place that decides whether a late response still belongs on screen.
 */
export type RelatedLoader = {
  model: RelatedModel;
  view: RelatedView;
  load: (mode: RelatedMode, requestedLimit: number) => Promise<boolean>;
  reload: (mode: RelatedMode) => Promise<boolean>;
  setStatus: (message: string, kind?: StatusKind) => void;
  showStatus: (mode: RelatedMode) => void;
};

export const createRelatedLoader = (
  root: HTMLElement,
  config: RelatedConfig,
  fetcher: RelatedFetch
): RelatedLoader => {
  const model = createRelatedModel();
  const view = createRelatedView(root);
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
        const response = await requestRelated(
          fetcher,
          config.endpoint,
          config.source,
          mode,
          requestedLimit
        );
        if (requestIDs[mode] !== requestID) {
          return false;
        }
        applyResponse(model, view, mode, requestedLimit, response);
        succeeded = true;
      } catch (error) {
        if (requestIDs[mode] !== requestID) {
          return false;
        }
        state.error = relatedErrorMessage(error);
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

  const reload = async (mode: RelatedMode): Promise<boolean> => {
    const pendingLoad = loadPromises[mode];
    if (pendingLoad) {
      await pendingLoad;
    }
    return load(mode, model.modes[mode].limit);
  };

  return { model, view, load, reload, setStatus, showStatus };
};
