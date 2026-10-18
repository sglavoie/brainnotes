import { type BriefCard, type BriefSlotKind } from "./api";
import {
  briefNotePathForRelPath,
  insightPromptMaxLength,
  relationshipLabel,
} from "./daily-brief-model";
import { queryAllElements, queryElement } from "./dom";

// SlotAvailability captures the page-level conditions that decide whether a
// card's actions can be offered, so hint building stays free of DOM lookups.
export type SlotAvailability = {
  stateUnreadable: boolean;
  editorConfigured: boolean;
  insightConfigured: boolean;
};

export type UnavailableHint = { open: string; another: string; insight: string };

export const findSlotElement = (root: HTMLElement, slot: BriefSlotKind): HTMLElement | null =>
  queryElement(root, `[data-brief-slot="${slot}"]`, HTMLElement);

export const findSlotStatusElement = (root: HTMLElement, slot: BriefSlotKind): HTMLElement | null =>
  queryElement(root, `[data-brief-slot-status="${slot}"]`, HTMLElement);

export const markButtonDefaultDisabled = (button: HTMLButtonElement): void => {
  button.setAttribute("data-brief-disabled-default", button.disabled ? "true" : "false");
};

export const applyButtonDisabledDefaults = (container: ParentNode): void => {
  for (const button of queryAllElements(container, "[data-brief-action]", HTMLButtonElement)) {
    markButtonDefaultDisabled(button);
  }
  for (const prompt of queryAllElements(container, "[data-brief-insight-prompt]", HTMLTextAreaElement)) {
    prompt.setAttribute("data-brief-disabled-default", prompt.disabled ? "true" : "false");
  }
};

export const createActionButton = (
  doc: Document,
  slot: BriefSlotKind,
  cardID: string,
  action: "open" | "another" | "insight",
  label: string,
  enabled: boolean
): HTMLButtonElement => {
  const button = doc.createElement("button");
  button.type = "button";
  button.className = "brief-action-button";
  button.setAttribute("data-brief-action", action);
  button.setAttribute(`data-brief-action-${action}`, "");
  button.setAttribute("data-brief-slot", slot);
  button.setAttribute("data-brief-card-id", cardID);
  button.textContent = label;
  button.disabled = !enabled;
  button.setAttribute("data-brief-disabled-default", enabled ? "false" : "true");
  return button;
};

export const createInsightPrompt = (
  doc: Document,
  slot: BriefSlotKind,
  cardID: string,
  enabled: boolean
): HTMLElement => {
  const field = doc.createElement("div");
  field.className = "brief-insight-prompt-field";
  const promptID = `brief-insight-prompt-${slot}-${cardID}`;
  const label = doc.createElement("label");
  label.className = "brief-insight-prompt-label";
  label.htmlFor = promptID;
  label.textContent = "Ask AI about this note";
  const controls = doc.createElement("div");
  controls.className = "brief-insight-prompt-controls";
  const prompt = doc.createElement("textarea");
  prompt.id = promptID;
  prompt.className = "brief-insight-prompt";
  prompt.rows = 2;
  prompt.maxLength = insightPromptMaxLength;
  prompt.placeholder = "Optional: ask a question or specify the output";
  prompt.disabled = !enabled;
  prompt.setAttribute("data-brief-insight-prompt", "");
  prompt.setAttribute("data-brief-disabled-default", enabled ? "false" : "true");
  const button = createActionButton(doc, slot, cardID, "insight", "Ask AI", enabled);
  controls.append(prompt, button);
  const hint = doc.createElement("span");
  hint.className = "brief-insight-prompt-hint";
  hint.textContent = "Leave blank for the suggested insight. Press Ctrl/⌘ + Enter to ask.";
  field.append(label, controls, hint);
  const disclosure = doc.createElement("details");
  disclosure.className = "brief-ai";
  const summary = doc.createElement("summary");
  summary.textContent = "Ask AI";
  disclosure.append(summary, field);
  return disclosure;
};

export const ensureInsightPanel = (cardEl: HTMLElement, slot: BriefSlotKind): HTMLElement => {
  const existing = queryElement(cardEl, "[data-brief-insight-panel]", HTMLElement);
  if (existing) {
    return existing;
  }
  const doc = cardEl.ownerDocument;
  const panel = doc.createElement("section");
  panel.className = "brief-insight";
  panel.setAttribute("data-brief-insight-panel", "");
  panel.hidden = true;

  const header = doc.createElement("div");
  header.className = "brief-insight-header";

  const title = doc.createElement("h5");
  title.className = "brief-insight-title";
  title.textContent = "AI insight";
  header.appendChild(title);

  const copy = doc.createElement("button");
  copy.className = "brief-insight-copy";
  copy.type = "button";
  copy.hidden = true;
  copy.disabled = true;
  copy.textContent = "Copy Markdown";
  copy.setAttribute("data-brief-insight-copy", "");
  copy.setAttribute("data-brief-slot", slot);
  header.appendChild(copy);

  const body = doc.createElement("div");
  body.className = "brief-insight-body";
  body.setAttribute("data-brief-insight-body", "");

  panel.append(header, body);
  cardEl.appendChild(panel);
  return panel;
};

export const renderInsight = (
  root: HTMLElement,
  slot: BriefSlotKind,
  cardID: string,
  renderedHTML: string,
  output: string
): void => {
  const slotEl = findSlotElement(root, slot);
  if (!slotEl) {
    return;
  }
  const cardEl = queryElement(
    slotEl,
    `[data-brief-card][data-brief-card-id="${cardID}"]`,
    HTMLElement
  );
  if (!cardEl) {
    return;
  }
  const panel = ensureInsightPanel(cardEl, slot);
  const body = queryElement(panel, "[data-brief-insight-body]", HTMLElement);
  const copy = queryElement(panel, "[data-brief-insight-copy]", HTMLButtonElement);
  if (!body || !copy) {
    return;
  }
  body.innerHTML = renderedHTML;
  copy.setAttribute("data-brief-copy-output", output);
  copy.hidden = false;
  copy.disabled = output.trim() === "";
  panel.hidden = false;
};

export const buildUnavailableHint = (
  availability: SlotAvailability,
  sourcePath: string
): UnavailableHint => {
  if (availability.stateUnreadable) {
    return {
      open: "Open is unavailable until brief state is readable.",
      another: "Another is unavailable until brief state is readable.",
      insight: "Ask AI is unavailable until brief state is readable.",
    };
  }
  return {
    open:
      sourcePath === ""
        ? "Open is unavailable because this note route could not be resolved."
        : availability.editorConfigured
          ? ""
          : "Open is disabled until the editor is configured.",
    another: "",
    insight: availability.insightConfigured
      ? ""
      : "Ask AI is unavailable until [agent].command is configured.",
  };
};

export const renderCard = (
  doc: Document,
  availability: SlotAvailability,
  slot: BriefSlotKind,
  card: BriefCard
): HTMLElement => {
  const cardEl = doc.createElement("div");
  cardEl.className = "brief-card";
  cardEl.setAttribute("data-brief-card", "");
  cardEl.setAttribute("data-brief-card-id", card.ID);

  appendCardHeading(doc, cardEl, slot, card);
  appendCardExplanation(doc, cardEl, slot, card);
  const sourcePath = briefNotePathForRelPath(card.Source.RelPath);

  if (card.Excerpt) {
    const excerpt = doc.createElement("blockquote");
    excerpt.className = "brief-card-excerpt";
    excerpt.textContent = card.Excerpt;
    cardEl.appendChild(excerpt);
  }

  const reasons = card.Metadata.Reasons.filter((reason) => reason.trim() !== "" && reason !== card.Explanation);
  if (reasons.length > 0) {
    const list = doc.createElement("ul");
    list.className = "brief-card-reasons";
    for (const reason of reasons) {
      const item = doc.createElement("li");
      item.textContent = reason;
      list.appendChild(item);
    }
    cardEl.appendChild(list);
  }

  const unavailable = buildUnavailableHint(availability, sourcePath);
  const actionRow = doc.createElement("div");
  actionRow.className = "brief-card-actions";
  actionRow.append(
    createActionButton(doc, slot, card.ID, "open", "Open", unavailable.open === ""),
    createActionButton(doc, slot, card.ID, "another", "Another", unavailable.another === "")
  );
  cardEl.appendChild(actionRow);
  cardEl.appendChild(createInsightPrompt(doc, slot, card.ID, unavailable.insight === ""));

  for (const [action, message] of Object.entries(unavailable)) {
    if (!message) continue;
    const hint = doc.createElement("p");
    hint.className = "brief-action-hint";
    hint.textContent = message;
    (action === "insight" ? cardEl.querySelector(".brief-ai") : cardEl)?.appendChild(hint);
  }

  discloseCardReasons(cardEl);
  ensureInsightPanel(cardEl, slot);
  return cardEl;
};

const discloseCardReasons = (card: HTMLElement): void => {
  const details = card.ownerDocument.createElement("details");
  details.className = "brief-card-why";
  const summary = card.ownerDocument.createElement("summary");
  summary.textContent = "Why this note?";
  details.append(summary);
  details.append(...card.querySelectorAll(".brief-card-path, .brief-card-explanation, .brief-card-meta, .brief-card-reasons"));
  card.querySelector(".brief-ai")?.before(details);
};

const appendCardHeading = (doc: Document, cardEl: HTMLElement, slot: BriefSlotKind, card: BriefCard): void => {
  const sourceTitle = card.Source.Title || card.Source.RelPath;
  const sourcePath = briefNotePathForRelPath(card.Source.RelPath);

  const heading = doc.createElement("h4");
  heading.className = "brief-card-title";
  let headingLink: HTMLAnchorElement | null = null;
  if (slot === "connect" && card.Candidate) {
    const candidatePath = briefNotePathForRelPath(card.Candidate.RelPath);
    const candidateTitle = card.Candidate.Title || card.Candidate.RelPath;
    if (candidatePath) {
      headingLink = doc.createElement("a");
      headingLink.textContent = candidateTitle;
      headingLink.href = candidatePath;
    } else {
      heading.textContent = candidateTitle;
    }
  } else if (sourcePath) {
    headingLink = doc.createElement("a");
    headingLink.textContent = sourceTitle;
    headingLink.href = sourcePath;
  } else {
    heading.textContent = sourceTitle;
  }
  if (headingLink) {
    heading.appendChild(headingLink);
  }
  cardEl.appendChild(heading);

  const path = doc.createElement("p");
  path.className = "brief-card-path";
  const pathCode = doc.createElement("code");
  pathCode.textContent = slot === "connect" && card.Candidate ? card.Candidate.RelPath : card.Source.RelPath;
  path.appendChild(pathCode);
  cardEl.appendChild(path);

};

const appendCardExplanation = (doc: Document, cardEl: HTMLElement, slot: BriefSlotKind, card: BriefCard): void => {
  const explanation = doc.createElement("p");
  explanation.className = "brief-card-explanation";
  explanation.textContent = card.Explanation;
  cardEl.appendChild(explanation);

  const relationship = relationshipLabel(card.Metadata.Relationship);
  if (slot === "connect" && relationship) {
    const relationshipEl = doc.createElement("p");
    relationshipEl.className = "brief-card-meta";
    relationshipEl.textContent = `Relationship: ${relationship}`;
    cardEl.appendChild(relationshipEl);
  }
  if (slot === "connect" && card.Metadata.Bridge) {
    const bridgeEl = doc.createElement("p");
    bridgeEl.className = "brief-card-meta";
    const bridgeCode = doc.createElement("code");
    bridgeCode.textContent = card.Metadata.Bridge;
    bridgeEl.append("Bridge: ", bridgeCode);
    cardEl.appendChild(bridgeEl);
  }

};
