import { withLibraryContext } from "./note-navigation";
import {
  RELATED_MAX_LIMIT,
  addRelatedCalendarDays,
  relatedModeDefinitions,
  relatedReasonLabel,
  relatedRelationshipLabel,
} from "./related-model";
import type { RelatedFeedback, RelatedMode, RelatedResponse, RelatedResult } from "./related-types";

export type RelatedView = {
  status: HTMLElement;
  retry: HTMLButtonElement;
  tabs: Record<RelatedMode, HTMLButtonElement>;
  panels: Record<RelatedMode, HTMLElement>;
  results: Record<RelatedMode, HTMLElement>;
  moreButtons: Record<RelatedMode, HTMLButtonElement>;
  feedbackDrawer: HTMLDetailsElement;
  feedbackSummary: HTMLElement;
  feedbackList: HTMLElement;
};

const relatedID = (kind: "tab" | "panel", mode: RelatedMode): string => `related-${kind}-${mode}`;

const createRetryButton = (doc: Document): HTMLButtonElement => {
  const retry = doc.createElement("button");
  retry.type = "button";
  retry.className = "related-card-action";
  retry.dataset.relatedRetry = "";
  retry.textContent = "Retry related notes";
  retry.hidden = true;
  return retry;
};

export const renderRelatedRetry = (view: RelatedView, available: boolean, busy: boolean): void => {
  // Browsers drop focus when disabling a button. Move it to the status before
  // retry starts, and never steal it back if the user moves elsewhere afterward.
  if ((busy || !available) && view.retry.ownerDocument.activeElement === view.retry) view.status.focus();
  view.retry.hidden = !available;
  view.retry.disabled = busy;
};

const createModePanel = (
  doc: Document,
  mode: RelatedMode,
  description: string
): { panel: HTMLElement; results: HTMLElement; more: HTMLButtonElement } => {
  const panel = doc.createElement("section");
  panel.id = relatedID("panel", mode);
  panel.className = "related-mode-panel";
  panel.setAttribute("role", "tabpanel");
  panel.setAttribute("aria-labelledby", relatedID("tab", mode));
  panel.setAttribute("data-related-panel", mode);
  panel.tabIndex = 0;
  panel.hidden = mode !== "balanced";

  const descriptionEl = doc.createElement("p");
  descriptionEl.className = "related-mode-description";
  descriptionEl.textContent = description;
  const results = doc.createElement("div");
  results.className = "related-results";
  results.setAttribute("data-related-results", mode);
  const more = doc.createElement("button");
  more.type = "button";
  more.className = "related-show-more";
  more.setAttribute("data-related-show-more", mode);
  more.textContent = "Show more";
  more.hidden = true;
  panel.append(descriptionEl, results, more);
  return { panel, results, more };
};

export const createRelatedView = (root: HTMLElement): RelatedView => {
  const doc = root.ownerDocument;
  const retry = createRetryButton(doc);
  const heading = root.querySelector("h2");
  const status = root.querySelector<HTMLElement>("[data-related-status]") || doc.createElement("p");
  status.className = "related-status";
  status.setAttribute("data-related-status", "");
  status.setAttribute("role", "status");
  status.setAttribute("aria-live", "polite");
  status.tabIndex = -1;

  const intro = doc.createElement("p");
  intro.className = "related-notes-intro";
  intro.textContent = "Explore nearby ideas without changing this note.";
  const tablist = doc.createElement("div");
  tablist.className = "related-tab-list";
  tablist.setAttribute("role", "tablist");
  tablist.setAttribute("aria-label", "Related note modes");

  const feedbackDrawer = doc.createElement("details");
  feedbackDrawer.className = "related-feedback-drawer";
  feedbackDrawer.setAttribute("data-related-feedback-drawer", "");
  const feedbackSummary = doc.createElement("summary");
  feedbackSummary.setAttribute("data-related-feedback-summary", "");
  feedbackSummary.textContent = "Feedback (0)";
  const feedbackList = doc.createElement("div");
  feedbackList.className = "related-feedback-list";
  feedbackList.setAttribute("data-related-feedback-list", "");
  const feedbackEmpty = doc.createElement("p");
  feedbackEmpty.className = "related-feedback-empty";
  feedbackEmpty.textContent = "No feedback yet.";
  feedbackList.appendChild(feedbackEmpty);
  feedbackDrawer.append(feedbackSummary, feedbackList);

  const panelsContainer = doc.createElement("div");
  panelsContainer.className = "related-panels";
  const tabs = {} as Record<RelatedMode, HTMLButtonElement>;
  const panels = {} as Record<RelatedMode, HTMLElement>;
  const results = {} as Record<RelatedMode, HTMLElement>;
  const moreButtons = {} as Record<RelatedMode, HTMLButtonElement>;

  for (const definition of relatedModeDefinitions) {
    const tab = doc.createElement("button");
    tab.type = "button";
    tab.id = relatedID("tab", definition.mode);
    tab.className = "related-tab";
    tab.setAttribute("role", "tab");
    tab.setAttribute("aria-controls", relatedID("panel", definition.mode));
    tab.setAttribute("aria-selected", definition.mode === "balanced" ? "true" : "false");
    tab.setAttribute("data-related-tab", definition.mode);
    tab.tabIndex = definition.mode === "balanced" ? 0 : -1;
    const label = doc.createElement("span");
    label.className = "related-tab-label";
    label.textContent = definition.label;
    const description = doc.createElement("span");
    description.className = "related-tab-description";
    description.textContent = definition.description;
    tab.append(label, description);
    tablist.appendChild(tab);

    const modePanel = createModePanel(doc, definition.mode, definition.description);
    panelsContainer.appendChild(modePanel.panel);
    tabs[definition.mode] = tab;
    panels[definition.mode] = modePanel.panel;
    results[definition.mode] = modePanel.results;
    moreButtons[definition.mode] = modePanel.more;
  }

  root.replaceChildren();
  if (heading) {
    root.appendChild(heading);
  } else {
    const fallbackHeading = doc.createElement("h2");
    fallbackHeading.textContent = "Related Notes";
    root.appendChild(fallbackHeading);
  }
  root.append(intro, tablist, status, retry, feedbackDrawer, panelsContainer);
  return { status, retry, tabs, panels, results, moreButtons, feedbackDrawer, feedbackSummary, feedbackList };
};

export const selectRelatedModeView = (view: RelatedView, activeMode: RelatedMode): void => {
  for (const definition of relatedModeDefinitions) {
    const selected = definition.mode === activeMode;
    view.tabs[definition.mode].setAttribute("aria-selected", selected ? "true" : "false");
    view.tabs[definition.mode].tabIndex = selected ? 0 : -1;
    view.panels[definition.mode].hidden = !selected;
  }
};

const actionButton = (doc: Document, label: string, action: string, candidate: string): HTMLButtonElement => {
  const button = doc.createElement("button");
  button.type = "button";
  button.className = "related-card-action";
  button.textContent = label;
  button.setAttribute("data-related-action", action);
  button.setAttribute("data-related-candidate", candidate);
  return button;
};

const renderLaterControl = (doc: Document, result: RelatedResult, serverDate: string): HTMLDetailsElement => {
  const details = doc.createElement("details");
  details.className = "related-later-control";
  details.setAttribute("data-related-later-control", result.candidate.key);
  const summary = doc.createElement("summary");
  summary.textContent = "Later";
  const options = doc.createElement("div");
  options.className = "related-later-options";
  const tomorrow = actionButton(doc, "Tomorrow", "later", result.candidate.key);
  tomorrow.setAttribute("data-related-until", addRelatedCalendarDays(serverDate, 1));
  const nextWeek = actionButton(doc, "Next week", "later", result.candidate.key);
  nextWeek.setAttribute("data-related-until", addRelatedCalendarDays(serverDate, 7));
  const form = doc.createElement("form");
  form.className = "related-later-custom";
  form.setAttribute("data-related-later-form", result.candidate.key);
  const label = doc.createElement("label");
  label.textContent = "Custom date";
  const input = doc.createElement("input");
  input.type = "date";
  input.name = "until";
  input.required = true;
  input.min = addRelatedCalendarDays(serverDate, 1);
  input.setAttribute("data-related-later-date", "");
  label.appendChild(input);
  const submit = doc.createElement("button");
  submit.type = "submit";
  submit.className = "related-card-action";
  submit.textContent = "Choose date";
  form.append(label, submit);
  options.append(tomorrow, nextWeek, form);
  details.append(summary, options);
  return details;
};

const renderRelatedCard = (doc: Document, result: RelatedResult, serverDate: string): HTMLElement => {
  const card = doc.createElement("article");
  card.className = "related-card";
  card.setAttribute("data-related-card", result.candidate.key);
  card.tabIndex = -1;
  const heading = doc.createElement("h3");
  heading.className = "related-card-title";
  const link = doc.createElement("a");
  link.href = withLibraryContext(result.candidate.route);
  link.textContent = result.candidate.title || result.candidate.path;
  link.setAttribute("data-related-view-candidate", result.candidate.key);
  heading.appendChild(link);
  const path = doc.createElement("p");
  path.className = "related-card-path";
  const pathCode = doc.createElement("code");
  pathCode.textContent = result.candidate.path;
  path.appendChild(pathCode);
  const relationship = doc.createElement("p");
  relationship.className = "related-card-relationship";
  relationship.textContent = relatedRelationshipLabel(result.relationship);
  card.append(heading, path, relationship);

  if (result.bridge) {
    const bridge = doc.createElement("p");
    bridge.className = "related-card-bridge";
    const bridgeLink = doc.createElement("a");
    bridgeLink.href = withLibraryContext(result.bridge.route);
    bridgeLink.textContent = result.bridge.title || result.bridge.path;
    bridgeLink.setAttribute("data-related-view-candidate", result.bridge.key);
    bridge.append("Via ", bridgeLink);
    card.appendChild(bridge);
  }
  if (result.excerpt !== "") {
    const excerpt = doc.createElement("blockquote");
    excerpt.className = "related-card-excerpt";
    excerpt.textContent = result.excerpt;
    card.appendChild(excerpt);
  }
  if (result.reasons.length > 0) {
    const reasons = doc.createElement("ul");
    reasons.className = "related-card-reasons";
    for (const reason of result.reasons.slice(0, 3)) {
      const item = doc.createElement("li");
      const label = doc.createElement("strong");
      label.textContent = `${relatedReasonLabel(reason.kind)}: `;
      const detail = doc.createElement("span");
      detail.textContent = reason.detail;
      item.append(label, detail);
      reasons.appendChild(item);
    }
    card.appendChild(reasons);
  }

  const actions = doc.createElement("div");
  actions.className = "related-card-actions";
  const useful = actionButton(doc, "Useful", "useful", result.candidate.key);
  useful.setAttribute("aria-pressed", result.useful ? "true" : "false");
  actions.append(
    useful,
    actionButton(doc, "Dismiss", "dismissed", result.candidate.key),
    renderLaterControl(doc, result, serverDate),
    actionButton(doc, "Open in editor", "open_editor", result.candidate.key)
  );
  card.appendChild(actions);
  return card;
};

export const renderRelatedFeedback = (view: RelatedView, feedback: RelatedFeedback[]): void => {
  view.feedbackSummary.textContent = `Feedback (${feedback.length})`;
  view.feedbackList.replaceChildren();
  if (feedback.length === 0) {
    const empty = view.feedbackList.ownerDocument.createElement("p");
    empty.className = "related-feedback-empty";
    empty.textContent = "No feedback yet.";
    view.feedbackList.appendChild(empty);
    return;
  }
  for (const entry of feedback) {
    const row = view.feedbackList.ownerDocument.createElement("div");
    row.className = "related-feedback-row";
    row.setAttribute("data-related-feedback-row", entry.candidate.key);
    const link = view.feedbackList.ownerDocument.createElement("a");
    link.href = withLibraryContext(entry.candidate.route);
    link.textContent = entry.candidate.title || entry.candidate.path;
    link.setAttribute("data-related-view-candidate", entry.candidate.key);
    const state = view.feedbackList.ownerDocument.createElement("span");
    state.className = "related-feedback-state";
    state.textContent = entry.state === "dismissed" ? "Dismissed" : entry.state === "later" ? "Later" : "Useful";
    if (entry.state === "later" && entry.until) {
      const deadline = view.feedbackList.ownerDocument.createElement("time");
      deadline.dateTime = entry.until;
      deadline.textContent = ` until ${entry.until}`;
      state.appendChild(deadline);
    }
    const clear = actionButton(view.feedbackList.ownerDocument, "Clear", "clear", entry.candidate.key);
    clear.classList.add("related-feedback-clear");
    row.append(link, state, clear);
    view.feedbackList.appendChild(row);
  }
};

export const renderRelatedResponse = (view: RelatedView, response: RelatedResponse, requestedLimit: number): void => {
  const container = view.results[response.mode];
  container.replaceChildren();
  if (response.results.length === 0) {
    const empty = container.ownerDocument.createElement("p");
    empty.className = "related-empty";
    empty.textContent = "No related notes are available in this mode.";
    container.appendChild(empty);
  } else {
    for (const result of response.results) {
      container.appendChild(renderRelatedCard(container.ownerDocument, result, response.serverDate));
    }
  }
  const more = view.moreButtons[response.mode];
  more.hidden = !response.hasMore || requestedLimit >= RELATED_MAX_LIMIT;
  more.disabled = false;
};

export const setRelatedModeBusy = (view: RelatedView, mode: RelatedMode, busy: boolean): void => {
  view.panels[mode].setAttribute("aria-busy", busy ? "true" : "false");
  view.moreButtons[mode].disabled = busy;
};

export const setRelatedCardBusy = (card: HTMLElement, busy: boolean): void => {
  card.setAttribute("aria-busy", busy ? "true" : "false");
  for (const control of card.querySelectorAll<HTMLInputElement | HTMLButtonElement>("button, input")) {
    control.disabled = busy;
  }
};

export const setRelatedFeedbackBusy = (row: HTMLElement, busy: boolean): void => {
  row.setAttribute("aria-busy", busy ? "true" : "false");
  for (const button of row.querySelectorAll<HTMLButtonElement>("button")) {
    button.disabled = busy;
  }
};
