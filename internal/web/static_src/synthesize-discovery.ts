import { setStatusText } from "./status";
import { synthesisSelectedKeys } from "./synthesize-model";
import {
  renderSynthesisCandidates,
  selectSynthesisSuggestionMode,
} from "./synthesize-render";
import { requestSynthesisSearch, requestSynthesisSuggestions } from "./synthesize-requests";
import {
  candidateFromRelatedResult,
  candidateFromSearchResult,
} from "./synthesize-sources";
import {
  errorMessage,
  modeLabel,
  SEARCH_EMPTY,
  SUGGESTIONS_EMPTY,
  type SynthesisRuntime,
} from "./synthesize-runtime";
import type { RelatedMode } from "./related-types";
import { loadFailureMessage } from "./request-outcome";

export type SynthesisDiscoveryActions = {
  selectMode: (mode: RelatedMode) => Promise<void>;
  runSearch: (query: string) => Promise<void>;
};

// createSynthesisSearch runs the vault search beside the suggestions. Only the
// newest search renders; an older answer landing late is dropped.
const createSynthesisSearch = (runtime: SynthesisRuntime): ((query: string) => Promise<void>) => {
  const { config, model, view } = runtime;
  let searchRequest = 0;
  return async (query: string): Promise<void> => {
    const trimmed = query.trim();
    if (trimmed === "") {
      searchRequest += 1;
      runtime.searchEntries = null;
      view.searchResults.replaceChildren();
      setStatusText(view.searchStatus, "Enter a search term to find notes.", "");
      return;
    }
    const requestID = searchRequest + 1;
    searchRequest = requestID;
    setStatusText(view.searchStatus, `Searching for ${trimmed}…`, "is-loading");
    try {
      const results = await requestSynthesisSearch(runtime.fetcher, config.searchEndpoint, trimmed);
      if (searchRequest !== requestID) {
        return;
      }
      const entries = results.flatMap((result) => {
        const candidate = candidateFromSearchResult(result);
        return candidate ? [candidate] : [];
      });
      runtime.rememberCandidates(entries);
      runtime.searchEntries = entries;
      renderSynthesisCandidates(
        view.searchResults,
        entries,
        synthesisSelectedKeys(model),
        config.anchor.key,
        true,
        SEARCH_EMPTY
      );
      const shown = view.searchResults.querySelectorAll("[data-synthesize-candidate]").length;
      setStatusText(
        view.searchStatus,
        shown === 0 ? SEARCH_EMPTY : `${shown} ${shown === 1 ? "match" : "matches"} for ${trimmed}.`,
        shown === 0 ? "is-empty" : "is-success"
      );
    } catch (error) {
      if (searchRequest !== requestID) {
        return;
      }
      runtime.searchEntries = null;
      view.searchResults.replaceChildren();
      setStatusText(view.searchStatus, errorMessage(error, loadFailureMessage("search results", "Search")), "is-error");
    }
  };
};

export const createSynthesisDiscoveryActions = (
  runtime: SynthesisRuntime
): SynthesisDiscoveryActions => {
  const { config, model, view } = runtime;
  const suggestionRequests: Record<RelatedMode, number> = {
    balanced: 0,
    rediscover: 0,
    connect: 0,
  };
  let activeMode: RelatedMode = "balanced";

  const showSuggestionCount = (mode: RelatedMode): void => {
    const shown = view.suggestionResults[mode].querySelectorAll("[data-synthesize-candidate]").length;
    setStatusText(
      view.suggestionStatus,
      shown === 0
        ? SUGGESTIONS_EMPTY
        : `${shown} ${shown === 1 ? "suggestion" : "suggestions"} in ${modeLabel(mode)}.`,
      shown === 0 ? "is-empty" : "is-success"
    );
  };

  const loadSuggestions = async (mode: RelatedMode): Promise<void> => {
    const requestID = suggestionRequests[mode] + 1;
    suggestionRequests[mode] = requestID;
    view.suggestionPanels[mode].setAttribute("aria-busy", "true");
    if (mode === activeMode) {
      setStatusText(view.suggestionStatus, `Loading ${modeLabel(mode)} suggestions…`, "is-loading");
    }
    try {
      const response = await requestSynthesisSuggestions(
        runtime.fetcher,
        config.relatedEndpoint,
        config.anchor.key,
        mode
      );
      if (suggestionRequests[mode] !== requestID) {
        return;
      }
      const entries = response.results.map(candidateFromRelatedResult);
      runtime.rememberCandidates(entries);
      runtime.suggestionEntries[mode] = entries;
      renderSynthesisCandidates(
        view.suggestionResults[mode],
        entries,
        synthesisSelectedKeys(model),
        config.anchor.key,
        false,
        SUGGESTIONS_EMPTY
      );
      if (mode === activeMode) {
        showSuggestionCount(mode);
      }
    } catch (error) {
      if (suggestionRequests[mode] !== requestID) {
        return;
      }
      if (mode === activeMode) {
        setStatusText(
          view.suggestionStatus,
          errorMessage(error, loadFailureMessage("suggestions", "the tab again")),
          "is-error"
        );
      }
    } finally {
      if (suggestionRequests[mode] === requestID) {
        view.suggestionPanels[mode].setAttribute("aria-busy", "false");
      }
    }
  };

  const selectMode = (mode: RelatedMode): Promise<void> => {
    activeMode = mode;
    selectSynthesisSuggestionMode(view, mode);
    if (runtime.suggestionEntries[mode] === null) {
      return loadSuggestions(mode);
    }
    showSuggestionCount(mode);
    return Promise.resolve();
  };

  return { selectMode, runSearch: createSynthesisSearch(runtime) };
};
