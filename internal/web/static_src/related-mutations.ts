import { requestRelatedItem } from "./related-actions";
import { relatedErrorMessage, type RelatedLoader } from "./related-loader";
import { relatedModeDefinitions, validateRelatedLaterDate, type RelatedModel } from "./related-model";
import {
  setRelatedCardBusy,
  setRelatedFeedbackBusy,
  type RelatedView,
} from "./related-render";
import type {
  RelatedConfig,
  RelatedFetch,
  RelatedItemAction,
  RelatedItemResponse,
  RelatedMode,
} from "./related-types";

export type RelatedMutation = (
  action: RelatedItemAction,
  candidate: string,
  until: string,
  origin: HTMLElement
) => Promise<void>;

const cardForCandidate = (
  view: RelatedView,
  mode: RelatedMode,
  candidate: string
): HTMLElement | null => {
  for (const card of view.results[mode].querySelectorAll<HTMLElement>("[data-related-card]")) {
    if (card.getAttribute("data-related-card") === candidate) {
      return card;
    }
  }
  return null;
};

/**
 * MutationTargets is everything about the page the mutation needs to know
 * before it starts, captured while the card is still on screen: a refresh
 * replaces the cards, so the neighbours focus falls back to cannot be looked up
 * afterwards.
 */
type MutationTargets = {
  card: HTMLElement | null;
  drawerRow: HTMLElement | null;
  next: string;
  previous: string;
};

const mutationTargets = (
  view: RelatedView,
  mode: RelatedMode,
  candidate: string,
  origin: HTMLElement
): MutationTargets => {
  const cards = Array.from(
    view.results[mode].querySelectorAll<HTMLElement>("[data-related-card]")
  );
  const index = cards.findIndex((item) => item.getAttribute("data-related-card") === candidate);
  const sibling = (offset: number): string =>
    cards[index + offset]?.getAttribute("data-related-card") || "";
  return {
    card: origin.closest<HTMLElement>("[data-related-card]"),
    drawerRow: origin.closest<HTMLElement>("[data-related-feedback-row]"),
    next: index >= 0 ? sibling(1) : "",
    previous: index > 0 ? sibling(-1) : "",
  };
};

const setMutationBusy = (targets: MutationTargets, busy: boolean): void => {
  // Clearing busy state is conditional on the element still being in the
  // document: a refresh replaces the card this mutation started from, and the
  // replacement is not busy.
  if (targets.card && (busy || targets.card.isConnected)) {
    setRelatedCardBusy(targets.card, busy);
  }
  if (targets.drawerRow && (busy || targets.drawerRow.isConnected)) {
    setRelatedFeedbackBusy(targets.drawerRow, busy);
  }
};

const focusAfterMutation = (
  loader: RelatedLoader,
  candidate: string,
  targets: MutationTargets
): void => {
  const { model, view } = loader;
  const fromDrawer = targets.drawerRow !== null;
  const preferred = fromDrawer ? [candidate] : [candidate, targets.next, targets.previous];
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

const markInactiveModesStale = (model: RelatedModel): void => {
  for (const definition of relatedModeDefinitions) {
    if (definition.mode !== model.activeMode) {
      model.modes[definition.mode].stale = true;
    }
  }
};

const feedbackOutcome = (action: RelatedItemAction, until: string): string => {
  switch (action) {
    case "dismissed":
      return "Dismissed note.";
    case "later":
      return `Hidden until ${until}.`;
    case "clear":
      return "Cleared feedback.";
    default:
      return "Marked note as useful.";
  }
};

const reportOpenOutcome = (loader: RelatedLoader, result: RelatedItemResponse): void => {
  // The warning is checked before the destination, because the server fills both
  // in on the one path that matters here: it answers with somewhere to go and
  // *then* fails to record the open. Navigating on that response would replace
  // the page that holds the only report of a broken recall store. Staying put
  // costs the reader one click — the card's own title is a link to the same
  // note — and is the only way the failure is ever seen in a navigating mode.
  if (result.warning) {
    loader.setStatus(
      result.destination
        ? `${result.warning} Open the note from its title link.`
        : `Editor opened. ${result.warning}`,
      "is-warning"
    );
    return;
  }
  // A server that does not start editors answered with somewhere to go, and the
  // open is already in the recall history, so leaving the page loses nothing.
  if (result.destination) {
    window.location.assign(result.destination);
    return;
  }
  loader.setStatus("Opened in editor and recorded in recall history.", "is-success");
};

const applyFeedbackOutcome = async (
  loader: RelatedLoader,
  action: RelatedItemAction,
  candidate: string,
  until: string,
  targets: MutationTargets
): Promise<void> => {
  const { model } = loader;
  markInactiveModesStale(model);
  const activeMode = model.activeMode;
  const activeState = model.modes[activeMode];
  const refreshed = await loader.reload(activeMode);
  if (!refreshed) {
    loader.setStatus(
      `Feedback was saved, but related notes could not be refreshed: ${
        activeState.error || "refresh failed"
      }`,
      "is-warning"
    );
    return;
  }
  loader.setStatus(feedbackOutcome(action, until), "is-success");
  focusAfterMutation(loader, candidate, targets);
};

export const createRelatedMutation = (
  loader: RelatedLoader,
  config: RelatedConfig,
  fetcher: RelatedFetch
): RelatedMutation => {
  const { model, view } = loader;
  return async (action, candidate, until, origin): Promise<void> => {
    if (config.source === "" || config.csrfToken === "" || candidate === "") {
      loader.setStatus("Related action unavailable.", "is-error");
      return;
    }
    const laterError = action === "later" ? validateRelatedLaterDate(model.serverDate, until) : "";
    if (laterError !== "") {
      loader.setStatus(laterError, "is-error");
      return;
    }
    const targets = mutationTargets(view, model.activeMode, candidate, origin);
    setMutationBusy(targets, true);
    // "Opening note…" rather than "Opening editor…": the panel is rendered by
    // every mode and only the server knows whether this ends in an editor or in
    // a navigation.
    loader.setStatus(action === "open_editor" ? "Opening note…" : "Saving feedback…", "is-loading");
    try {
      const result = await requestRelatedItem(fetcher, config.itemEndpoint, {
        source: config.source,
        candidate,
        action,
        token: config.csrfToken,
        ...(until ? { until } : {}),
      });
      if (action === "open_editor") {
        reportOpenOutcome(loader, result);
        return;
      }
      await applyFeedbackOutcome(loader, action, candidate, until, targets);
    } catch (error) {
      loader.setStatus(relatedErrorMessage(error), "is-error");
    } finally {
      setMutationBusy(targets, false);
    }
  };
};
