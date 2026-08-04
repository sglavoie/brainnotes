import {
  buildBriefInsightForm,
  buildBriefItemForm,
  buildBriefViewForm,
  normalizeBriefInsightResponse,
  normalizeBriefItemResponse,
  normalizeBriefViewResponse,
  normalizeManagementErrorMessage,
  type BriefCard,
  type BriefItemAction,
  type BriefItemResult,
  type BriefRollingActivity,
  type BriefSlotKind,
  type BriefSlotResult,
} from "./api";
import { queryAllElements, queryElement } from "./dom";
import { notePathForKey } from "./filters";
import { setStatusText, type StatusKind } from "./status";

type FetchLike = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;
type ConfirmLike = (message: string) => boolean;
type ClipboardWriter = (text: string) => Promise<void>;

export type DailyBriefDeps = {
  fetch?: FetchLike;
  confirm?: ConfirmLike;
  writeClipboard?: ClipboardWriter;
};

export type DailyBriefConfig = {
  viewEndpoint: string;
  itemEndpoint: string;
  insightEndpoint: string;
  csrfToken: string;
  insightEnabled: boolean;
};

export const readDailyBriefConfig = (root: HTMLElement): DailyBriefConfig => {
  const insightValue = (root.getAttribute("data-brief-insight-enabled") || "").toLowerCase();
  return {
    viewEndpoint: root.getAttribute("data-brief-view-endpoint") || "/api/brief/view",
    itemEndpoint: root.getAttribute("data-brief-item-endpoint") || "/api/brief/item",
    insightEndpoint: root.getAttribute("data-brief-insight-endpoint") || "/api/brief/insight",
    csrfToken: root.getAttribute("data-brief-csrf") || "",
    insightEnabled: insightValue === "true" || insightValue === "1",
  };
};

const slotOrder: BriefSlotKind[] = ["continue", "rediscover", "connect"];
const statusClasses = ["is-success", "is-error", "is-loading", "is-empty"];
const insightPromptMaxLength = 2000;

const normalizeSlot = (value: string): BriefSlotKind | "" => {
  switch ((value || "").toLowerCase()) {
    case "continue":
    case "rediscover":
    case "connect":
      return value.toLowerCase() as BriefSlotKind;
    default:
      return "";
  }
};

const slotLabel = (slot: BriefSlotKind): string => {
  switch (slot) {
    case "continue":
      return "Continue";
    case "rediscover":
      return "Rediscover";
    case "connect":
      return "Connect";
  }
};

const unavailableTitle = (slot: BriefSlotKind): string => {
  switch (slot) {
    case "continue":
      return "No substantive recent note";
    case "rediscover":
      return "No rediscover candidate";
    case "connect":
      return "No valid connection";
  }
};

const relationshipLabel = (relationship: string): string => {
  switch ((relationship || "").toLowerCase()) {
    case "inbound":
      return "Inbound link";
    case "outbound":
      return "Outbound link";
    case "shared-neighbor":
      return "Shared neighbor";
    default:
      return relationship;
  }
};

// briefNoteKeyFromRelPath mirrors server route-key generation: trim, normalize
// slashes, strip one trailing .md, reject traversal/empty segments, lowercase.
export const briefNoteKeyFromRelPath = (relPath: string): string => {
  const trimmed = (relPath || "").trim().replace(/\\/g, "/");
  if (trimmed === "") {
    return "";
  }
  const withoutExt = trimmed.replace(/\.md$/i, "");
  const segments: string[] = [];
  for (const segment of withoutExt.split("/")) {
    if (segment === "" || segment === "." || segment === "..") {
      return "";
    }
    segments.push(segment.toLowerCase());
  }
  return segments.join("/");
};

const briefNotePathForRelPath = (relPath: string): string => {
  const key = briefNoteKeyFromRelPath(relPath);
  return key ? notePathForKey(key) : "";
};

const jsonPostRequest = (csrfToken: string, body: string): RequestInit => ({
  method: "POST",
  headers: {
    Accept: "application/json",
    "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
    "X-BN-CSRF": csrfToken,
  },
  body,
});

const readResponsePayload = async (resp: Response): Promise<unknown> => {
  const contentType = resp.headers?.get("Content-Type") || "";
  try {
    if (contentType.includes("application/json")) {
      return await resp.json();
    }
    return await resp.text();
  } catch {
    return "";
  }
};

const insightDisclosureMessage = [
  "Ask AI sends this card's context and your optional prompt to your configured [agent].command.",
  "BrainNotes does not store the prompt or AI output. Continue?",
].join("\n");

export const initDailyBrief = (deps: DailyBriefDeps = {}): void => {
  const root = queryElement(document, "[data-brief-root]", HTMLElement);
  if (!root) {
    return;
  }

  const config = readDailyBriefConfig(root);
  const fetcher = deps.fetch || fetch;
  const confirmer = deps.confirm || ((message: string) => window.confirm(message));
  const clipboardWrite =
    deps.writeClipboard ||
    (navigator.clipboard && typeof navigator.clipboard.writeText === "function"
      ? (text: string) => navigator.clipboard.writeText(text)
      : undefined);

  const globalStatusEl = queryElement(root, "#brief-global-status", HTMLElement);
  const visitDaysEl = queryElement(root, "[data-brief-activity-visit-days]", HTMLElement);
  const openCountEl = queryElement(root, "[data-brief-activity-open-count]", HTMLElement);

  const slotElementFor = (slot: BriefSlotKind): HTMLElement | null =>
    queryElement(root, `[data-brief-slot="${slot}"]`, HTMLElement);

  const slotStatusElementFor = (slot: BriefSlotKind): HTMLElement | null =>
    queryElement(root, `[data-brief-slot-status="${slot}"]`, HTMLElement);

  const setGlobalStatus = (message: string, kind: StatusKind = ""): void => {
    setStatusText(globalStatusEl, message || " ", kind, statusClasses);
  };

  const setSlotStatus = (slot: BriefSlotKind, message: string, kind: StatusKind = ""): void => {
    setStatusText(slotStatusElementFor(slot), message || " ", kind, statusClasses);
  };

  const hasStateUnreadable = (): boolean =>
    queryElement(root, "[data-brief-state-unreadable]", HTMLElement) !== null;

  const hasEditorConfigured = (): boolean =>
    queryElement(root, "[data-brief-editor-unconfigured]", HTMLElement) === null;

  const insightConfigured = (): boolean => config.insightEnabled;

  const markButtonDefaultDisabled = (button: HTMLButtonElement): void => {
    button.setAttribute("data-brief-disabled-default", button.disabled ? "true" : "false");
  };

  const applyButtonDisabledDefaults = (container: ParentNode): void => {
    for (const button of queryAllElements(container, "[data-brief-action]", HTMLButtonElement)) {
      markButtonDefaultDisabled(button);
    }
    for (const prompt of queryAllElements(container, "[data-brief-insight-prompt]", HTMLTextAreaElement)) {
      prompt.setAttribute("data-brief-disabled-default", prompt.disabled ? "true" : "false");
    }
  };

  applyButtonDisabledDefaults(root);

  const openedCards = new Set<string>();
  const inflightInsight = new Map<BriefSlotKind, number>();
  let insightCounter = 0;
  let insightDisclosureAccepted = false;

  const setSlotBusy = (slot: BriefSlotKind, busy: boolean): void => {
    const slotEl = slotElementFor(slot);
    if (!slotEl) {
      return;
    }
    slotEl.setAttribute("aria-busy", busy ? "true" : "false");
    slotEl.classList.toggle("is-busy", busy);
    for (const button of queryAllElements(slotEl, "[data-brief-action]", HTMLButtonElement)) {
      const defaultDisabled = button.getAttribute("data-brief-disabled-default") === "true";
      button.disabled = busy || defaultDisabled;
    }
    for (const prompt of queryAllElements(slotEl, "[data-brief-insight-prompt]", HTMLTextAreaElement)) {
      const defaultDisabled = prompt.getAttribute("data-brief-disabled-default") === "true";
      prompt.disabled = busy || defaultDisabled;
    }
  };

  const setSlotsBusy = (slots: BriefSlotKind[], busy: boolean): void => {
    for (const slot of slots) {
      setSlotBusy(slot, busy);
    }
  };

  const markCardOpened = (slot: BriefSlotKind, cardID: string): void => {
    const slotEl = slotElementFor(slot);
    if (!slotEl) {
      return;
    }
    const openButton = queryElement(
      slotEl,
      `[data-brief-action-open][data-brief-card-id="${cardID}"]`,
      HTMLButtonElement
    );
    if (!openButton) {
      return;
    }
    openButton.textContent = "Opened";
    openButton.classList.add("is-opened");
  };

  const updateActivity = (activity: BriefRollingActivity): void => {
    if (visitDaysEl) {
      visitDaysEl.textContent = String(activity.Last7Days.VisitDays);
    }
    if (openCountEl) {
      openCountEl.textContent = String(activity.Last7Days.OpenCount);
    }
  };

  const createActionButton = (
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

  const createInsightPrompt = (
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
    return field;
  };

  const ensureInsightPanel = (cardEl: HTMLElement, slot: BriefSlotKind): HTMLElement => {
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

  const renderInsight = (
    slot: BriefSlotKind,
    cardID: string,
    renderedHTML: string,
    output: string
  ): void => {
    const slotEl = slotElementFor(slot);
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

  const buildUnavailableHint = (
    slot: BriefSlotKind,
    sourcePath: string
  ): { open: string; another: string; insight: string } => {
    if (hasStateUnreadable()) {
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
          : hasEditorConfigured()
            ? ""
            : "Open is disabled until the editor is configured.",
      another: "",
      insight: insightConfigured() ? "" : "Ask AI is unavailable until [agent].command is configured.",
    };
  };

  const renderCard = (slot: BriefSlotKind, card: BriefCard): HTMLElement => {
    const doc = root.ownerDocument;
    const cardEl = doc.createElement("div");
    cardEl.className = "brief-card";
    cardEl.setAttribute("data-brief-card", "");
    cardEl.setAttribute("data-brief-card-id", card.ID);

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

    const unavailable = buildUnavailableHint(slot, sourcePath);
    const actionRow = doc.createElement("div");
    actionRow.className = "brief-card-actions";
    actionRow.append(
      createActionButton(doc, slot, card.ID, "open", "Open", unavailable.open === ""),
      createActionButton(doc, slot, card.ID, "another", "Another", unavailable.another === "")
    );
    cardEl.appendChild(actionRow);
    cardEl.appendChild(createInsightPrompt(doc, slot, card.ID, unavailable.insight === ""));

    if (unavailable.open) {
      const hint = doc.createElement("p");
      hint.className = "brief-action-hint";
      hint.textContent = unavailable.open;
      cardEl.appendChild(hint);
    }
    if (unavailable.another) {
      const hint = doc.createElement("p");
      hint.className = "brief-action-hint";
      hint.textContent = unavailable.another;
      cardEl.appendChild(hint);
    }
    if (unavailable.insight) {
      const hint = doc.createElement("p");
      hint.className = "brief-action-hint";
      hint.textContent = unavailable.insight;
      cardEl.appendChild(hint);
    }

    ensureInsightPanel(cardEl, slot);
    return cardEl;
  };

  const replaceSlotContent = (slotResult: BriefSlotResult): void => {
    const slot = normalizeSlot(slotResult.Slot);
    if (!slot) {
      return;
    }
    const slotEl = slotElementFor(slot);
    if (!slotEl) {
      return;
    }

    inflightInsight.delete(slot);

    const header = queryElement(slotEl, ".panel-header", HTMLElement);
    if (!header) {
      return;
    }
    while (header.nextSibling) {
      slotEl.removeChild(header.nextSibling);
    }

    if (slotResult.Card) {
      slotEl.appendChild(renderCard(slot, slotResult.Card));
      if (openedCards.has(slotResult.Card.ID)) {
        markCardOpened(slot, slotResult.Card.ID);
      }
    } else {
      const unavailableMessage =
        slotResult.Unavailable?.Message || `${slotLabel(slot)} is unavailable for the current brief.`;
      const title = unavailableTitle(slot);
      const h4 = slotEl.ownerDocument.createElement("h4");
      h4.textContent = title;
      slotEl.appendChild(h4);
      const empty = slotEl.ownerDocument.createElement("p");
      empty.className = "review-empty brief-slot-empty";
      empty.setAttribute("data-brief-slot-empty", slot);
      empty.setAttribute("role", "status");
      empty.textContent = unavailableMessage;
      slotEl.appendChild(empty);
    }

    const status = slotEl.ownerDocument.createElement("p");
    status.className = "review-status brief-slot-status";
    status.setAttribute("data-brief-slot-status", slot);
    status.setAttribute("role", "status");
    status.setAttribute("aria-live", "polite");
    status.textContent = " ";
    slotEl.appendChild(status);

    applyButtonDisabledDefaults(slotEl);
  };

  const applyAnotherPayload = (result: BriefItemResult, requestedSlot: BriefSlotKind): BriefSlotKind[] => {
    const changed = new Set<BriefSlotKind>();
    for (const slotResult of result.affected) {
      const slot = normalizeSlot(slotResult.Slot);
      if (!slot) {
        continue;
      }
      replaceSlotContent(slotResult);
      changed.add(slot);
    }

    if (result.daily && result.daily.Slots.length > 0) {
      const ensureSlot = (slot: BriefSlotKind): void => {
        if (changed.has(slot)) {
          return;
        }
        const match = result.daily?.Slots.find((candidate) => normalizeSlot(candidate.Slot) === slot);
        if (!match) {
          return;
        }
        replaceSlotContent(match);
        changed.add(slot);
      };
      ensureSlot(requestedSlot);
      if (requestedSlot === "continue") {
        ensureSlot("connect");
      }
    }
    return Array.from(changed);
  };

  const readButtonSlot = (button: HTMLButtonElement): BriefSlotKind | "" =>
    normalizeSlot(button.getAttribute("data-brief-slot") || "");

  const readButtonCardID = (button: HTMLButtonElement): string =>
    button.getAttribute("data-brief-card-id") || "";

  const runOpen = async (button: HTMLButtonElement): Promise<void> => {
    const slot = readButtonSlot(button);
    const cardID = readButtonCardID(button);
    if (!slot || !cardID || !config.csrfToken) {
      setGlobalStatus("Open action unavailable.", "is-error");
      return;
    }

    setSlotsBusy([slot], true);
    setSlotStatus(slot, "Opening editor...", "is-loading");
    try {
      const body = buildBriefItemForm({ cardID, action: "open", token: config.csrfToken }).toString();
      const resp = await fetcher(config.itemEndpoint, jsonPostRequest(config.csrfToken, body));
      const payload = await readResponsePayload(resp);
      if (!resp.ok) {
        setSlotStatus(slot, normalizeManagementErrorMessage(resp.status, payload), "is-error");
        return;
      }
      const result = normalizeBriefItemResponse(payload);
      if (!result.ok) {
        setSlotStatus(slot, result.message || "Open failed.", "is-error");
        return;
      }
      updateActivity(result.activity);
      openedCards.add(cardID);
      markCardOpened(slot, cardID);
      setSlotStatus(slot, "Opened in editor. Refresh after saving changes.", "is-success");
      setGlobalStatus("Brief activity updated.", "is-success");
    } catch {
      setSlotStatus(slot, "Open failed.", "is-error");
    } finally {
      setSlotsBusy([slot], false);
    }
  };

  const runAnother = async (button: HTMLButtonElement): Promise<void> => {
    const slot = readButtonSlot(button);
    const cardID = readButtonCardID(button);
    if (!slot || !cardID || !config.csrfToken) {
      setGlobalStatus("Another action unavailable.", "is-error");
      return;
    }
    const busySlots: BriefSlotKind[] = slot === "continue" ? ["continue", "connect"] : [slot];

    setSlotsBusy(busySlots, true);
    setSlotStatus(slot, "Finding another card...", "is-loading");
    try {
      const body = buildBriefItemForm({ cardID, action: "another", token: config.csrfToken }).toString();
      const resp = await fetcher(config.itemEndpoint, jsonPostRequest(config.csrfToken, body));
      const payload = await readResponsePayload(resp);
      if (!resp.ok) {
        setSlotStatus(slot, normalizeManagementErrorMessage(resp.status, payload), "is-error");
        return;
      }
      const result = normalizeBriefItemResponse(payload);
      if (!result.ok) {
        setSlotStatus(slot, result.message || "Another failed.", "is-error");
        return;
      }

      updateActivity(result.activity);
      const changedSlots = applyAnotherPayload(result, slot);
      const statusMessage =
        result.message ||
        (slot === "continue" && changedSlots.includes("connect")
          ? "Updated Continue and Connect."
          : `Updated ${slotLabel(slot)}.`);
      setSlotStatus(slot, statusMessage, "is-success");
      setGlobalStatus(statusMessage, "is-success");

      const slotEl = slotElementFor(slot);
      const nextFocus =
        (slotEl && queryElement(slotEl, "[data-brief-action-another]", HTMLButtonElement)) ||
        (slotEl && queryElement(slotEl, "[data-brief-action]", HTMLButtonElement));
      nextFocus?.focus({ preventScroll: true });
    } catch {
      setSlotStatus(slot, "Another failed.", "is-error");
    } finally {
      setSlotsBusy(busySlots, false);
    }
  };

  const runInsight = async (button: HTMLButtonElement): Promise<void> => {
    const slot = readButtonSlot(button);
    const cardID = readButtonCardID(button);
    if (!slot || !cardID || !config.csrfToken) {
      setGlobalStatus("Ask AI is unavailable.", "is-error");
      return;
    }
    const cardEl = button.closest<HTMLElement>("[data-brief-card]");
    const prompt = cardEl
      ? queryElement(cardEl, "[data-brief-insight-prompt]", HTMLTextAreaElement)?.value.trim() || ""
      : "";
    if (!insightDisclosureAccepted) {
      insightDisclosureAccepted = confirmer(insightDisclosureMessage);
      if (!insightDisclosureAccepted) {
        setSlotStatus(slot, "Ask AI canceled.", "is-empty");
        return;
      }
    }

    setSlotsBusy([slot], true);
    setSlotStatus(slot, "Generating insight...", "is-loading");
    const requestID = insightCounter + 1;
    insightCounter = requestID;
    inflightInsight.set(slot, requestID);

    try {
      const body = buildBriefInsightForm({ cardID, prompt, token: config.csrfToken }).toString();
      const resp = await fetcher(
        config.insightEndpoint,
        jsonPostRequest(config.csrfToken, body)
      );
      const payload = await readResponsePayload(resp);
      if (inflightInsight.get(slot) !== requestID) {
        return;
      }
      if (!resp.ok) {
        setSlotStatus(slot, normalizeManagementErrorMessage(resp.status, payload), "is-error");
        return;
      }

      const result = normalizeBriefInsightResponse(payload);
      if (!result.ok) {
        setSlotStatus(slot, result.message || "Ask AI failed.", "is-error");
        return;
      }
      if (!result.enabled) {
        setSlotStatus(slot, result.message || "Ask AI is unavailable.", "is-error");
        return;
      }
      if (result.card_id && result.card_id !== cardID) {
        setSlotStatus(slot, "Card changed while insight was running. Try again.", "is-error");
        return;
      }

      renderInsight(slot, cardID, result.rendered_html, result.output);
      setSlotStatus(slot, "Insight ready.", "is-success");
    } catch {
      if (inflightInsight.get(slot) === requestID) {
        setSlotStatus(slot, "Ask AI failed.", "is-error");
      }
    } finally {
      if (inflightInsight.get(slot) === requestID) {
        inflightInsight.delete(slot);
        setSlotsBusy([slot], false);
      }
    }
  };

  const runCopyInsight = async (button: HTMLButtonElement): Promise<void> => {
    const slot = normalizeSlot(button.getAttribute("data-brief-slot") || "");
    if (!slot) {
      return;
    }
    const payload = button.getAttribute("data-brief-copy-output") || "";
    if (payload.trim() === "") {
      setSlotStatus(slot, "Nothing to copy.", "is-error");
      return;
    }
    if (!clipboardWrite) {
      setSlotStatus(slot, "Clipboard unavailable.", "is-error");
      return;
    }
    button.disabled = true;
    try {
      await clipboardWrite(payload);
      setSlotStatus(slot, "Insight markdown copied.", "is-success");
    } catch {
      setSlotStatus(slot, "Copy failed.", "is-error");
    } finally {
      button.disabled = false;
    }
  };

  root.addEventListener("click", (event) => {
    const target = event.target;
    if (!(target instanceof Element)) {
      return;
    }
    const copy = target.closest<HTMLButtonElement>("[data-brief-insight-copy]");
    if (copy && root.contains(copy)) {
      void runCopyInsight(copy);
      return;
    }

    const open = target.closest<HTMLButtonElement>("[data-brief-action-open]");
    if (open && root.contains(open)) {
      void runOpen(open);
      return;
    }

    const another = target.closest<HTMLButtonElement>("[data-brief-action-another]");
    if (another && root.contains(another)) {
      void runAnother(another);
      return;
    }

    const insight = target.closest<HTMLButtonElement>("[data-brief-action-insight]");
    if (insight && root.contains(insight)) {
      void runInsight(insight);
    }
  });
  root.addEventListener("keydown", (event) => {
    if (event.key !== "Enter" || (!event.ctrlKey && !event.metaKey)) {
      return;
    }
    const target = event.target;
    if (!(target instanceof HTMLTextAreaElement) || !target.matches("[data-brief-insight-prompt]")) {
      return;
    }
    const cardEl = target.closest<HTMLElement>("[data-brief-card]");
    const button = cardEl && queryElement(cardEl, "[data-brief-action-insight]", HTMLButtonElement);
    if (!button || button.disabled) {
      return;
    }
    event.preventDefault();
    void runInsight(button);
  });

  const recordView = async (): Promise<void> => {
    if (!config.csrfToken) {
      return;
    }
    try {
      const body = buildBriefViewForm({ token: config.csrfToken }).toString();
      const resp = await fetcher(config.viewEndpoint, jsonPostRequest(config.csrfToken, body));
      const payload = await readResponsePayload(resp);
      if (!resp.ok) {
        setGlobalStatus(normalizeManagementErrorMessage(resp.status, payload), "is-error");
        return;
      }
      const result = normalizeBriefViewResponse(payload);
      if (!result.ok) {
        setGlobalStatus(result.message || "Brief activity unavailable.", "is-error");
        return;
      }
      updateActivity(result.activity);
    } catch {
      setGlobalStatus("Brief activity unavailable.", "is-error");
    }
  };

  for (const slot of slotOrder) {
    const slotEl = slotElementFor(slot);
    if (!slotEl) {
      continue;
    }
    const cardEl = queryElement(slotEl, "[data-brief-card]", HTMLElement);
    if (!cardEl) {
      continue;
    }
    ensureInsightPanel(cardEl, slot);
  }

  void recordView();
};
