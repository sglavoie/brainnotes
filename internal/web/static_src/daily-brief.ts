import { confirmAction } from "./action-confirm";
import {
  type BriefItemResult,
  type BriefRollingActivity,
  type BriefSlotKind,
  type BriefSlotResult,
} from "./api";
import {
  recordView,
  runAnother,
  runCopyInsight,
  runInsight,
  runOpen,
  type BriefActionContext,
} from "./daily-brief-actions";
import {
  normalizeSlot,
  readDailyBriefConfig,
  slotLabel,
  slotOrder,
  statusClasses,
  unavailableTitle,
  type DailyBriefDeps,
} from "./daily-brief-model";
import {
  applyButtonDisabledDefaults,
  ensureInsightPanel,
  findSlotElement,
  findSlotStatusElement,
  renderCard,
  type SlotAvailability,
} from "./daily-brief-render";
import { queryAllElements, queryElement } from "./dom";
import { setStatusText, type StatusKind } from "./status";
import { isSubmitChord } from "./submit-chord";

export { briefNoteKeyFromRelPath, readDailyBriefConfig } from "./daily-brief-model";
export type { DailyBriefConfig, DailyBriefDeps } from "./daily-brief-model";

export const initDailyBrief = (deps: DailyBriefDeps = {}): void => {
  const root = queryElement(document, "[data-brief-root]", HTMLElement);
  if (!root) {
    return;
  }

  const config = readDailyBriefConfig(root);
  const request = deps.fetch || fetch;
  // Keep the browser's native fetch detached from BriefActionContext. Calling
  // it as ctx.fetcher(...) must not substitute that context as fetch's receiver.
  const fetcher = (input: RequestInfo | URL, init?: RequestInit): Promise<Response> =>
    request(input, init);
  const confirmer = deps.confirm || ((message: string, returnFocus?: Element | null) => confirmAction({ title: "Send context to Ask AI?", description: message, confirmLabel: "Send context", danger: false }, root.ownerDocument, returnFocus));
  const clipboardWrite =
    deps.writeClipboard ||
    (navigator.clipboard && typeof navigator.clipboard.writeText === "function"
      ? (text: string) => navigator.clipboard.writeText(text)
      : undefined);

  const globalStatusEl = queryElement(root, "#brief-global-status", HTMLElement);
  const visitDaysEl = queryElement(root, "[data-brief-activity-visit-days]", HTMLElement);
  const openCountEl = queryElement(root, "[data-brief-activity-open-count]", HTMLElement);


  const setGlobalStatus = (message: string, kind: StatusKind = ""): void => {
    setStatusText(globalStatusEl, message || " ", kind, statusClasses);
  };

  const setSlotStatus = (slot: BriefSlotKind, message: string, kind: StatusKind = ""): void => {
    setStatusText(findSlotStatusElement(root, slot), message || " ", kind, statusClasses);
  };

  applyButtonDisabledDefaults(root);

  const openedCards = new Set<string>();
  const inflightInsight = new Map<BriefSlotKind, number>();

  const setSlotsBusy = (slots: BriefSlotKind[], busy: boolean): void => {
    for (const slot of slots) {
      setSlotBusy(root, slot, busy);
    }
  };

  const updateActivity = (activity: BriefRollingActivity): void => {
    if (visitDaysEl) {
      visitDaysEl.textContent = String(activity.Last7Days.VisitDays);
    }
    if (openCountEl) {
      openCountEl.textContent = String(activity.Last7Days.OpenCount);
    }
  };

  const ctx: BriefActionContext = {
    root,
    config,
    fetcher,
    confirmer,
    clipboardWrite,
    openedCards,
    inflightInsight,
    insightCounter: 0,
    insightDisclosureAccepted: false,
    setGlobalStatus,
    setSlotStatus,
    setSlotsBusy,
    updateActivity,
    markCardOpened: (slot, cardID) => markCardOpened(root, slot, cardID),
    applyAnotherPayload: (result, slot) => applyAnotherPayload(ctx, result, slot),
  };

  bindBriefEvents(ctx);

  prepareInsightPanels(root);

  void recordView(ctx);
};

const setSlotBusy = (root: HTMLElement, slot: BriefSlotKind, busy: boolean): void => {

  const slotEl = findSlotElement(root, slot);
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

const markCardOpened = (root: HTMLElement, slot: BriefSlotKind, cardID: string): void => {

  const slotEl = findSlotElement(root, slot);
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

const replaceSlotContent = (ctx: BriefActionContext, slotResult: BriefSlotResult): void => {
  const { root, config, inflightInsight, openedCards, markCardOpened } = ctx;
  const readAvailability = (): SlotAvailability => ({
    stateUnreadable: root.querySelector("[data-brief-state-unreadable]") !== null,
    editorConfigured: root.querySelector("[data-brief-editor-unconfigured]") === null,
    insightConfigured: config.insightEnabled,
  });
  const slot = normalizeSlot(slotResult.Slot);
  if (!slot) {
    return;
  }
  const slotEl = findSlotElement(root, slot);
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
    slotEl.appendChild(renderCard(root.ownerDocument, readAvailability(), slot, slotResult.Card));
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

const applyAnotherPayload = (ctx: BriefActionContext, result: BriefItemResult, requestedSlot: BriefSlotKind): BriefSlotKind[] => {

  const changed = new Set<BriefSlotKind>();
  for (const slotResult of result.affected) {
    const slot = normalizeSlot(slotResult.Slot);
    if (!slot) {
      continue;
    }
    replaceSlotContent(ctx, slotResult);
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
      replaceSlotContent(ctx, match);
      changed.add(slot);
    };
    ensureSlot(requestedSlot);
    if (requestedSlot === "continue") {
      ensureSlot("connect");
    }
  }
  return Array.from(changed);
};

const bindBriefEvents = (ctx: BriefActionContext): void => {
  const { root } = ctx;
  root.addEventListener("click", (event) => {
    const target = event.target;
    if (!(target instanceof Element)) {
      return;
    }
    const copy = target.closest<HTMLButtonElement>("[data-brief-insight-copy]");
    if (copy && root.contains(copy)) {
      void runCopyInsight(ctx, copy);
      return;
    }

    const open = target.closest<HTMLButtonElement>("[data-brief-action-open]");
    if (open && root.contains(open)) {
      void runOpen(ctx, open);
      return;
    }

    const another = target.closest<HTMLButtonElement>("[data-brief-action-another]");
    if (another && root.contains(another)) {
      void runAnother(ctx, another);
      return;
    }

    const insight = target.closest<HTMLButtonElement>("[data-brief-action-insight]");
    if (insight && root.contains(insight)) {
      void runInsight(ctx, insight);
    }
  });
  root.addEventListener("keydown", (event) => {
    if (!isSubmitChord(event)) {
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
    void runInsight(ctx, button);
  });

};

const prepareInsightPanels = (root: HTMLElement): void => {
  for (const slot of slotOrder) {
    const slotEl = findSlotElement(root, slot);
    if (!slotEl) {
      continue;
    }
    const cardEl = queryElement(slotEl, "[data-brief-card]", HTMLElement);
    if (!cardEl) {
      continue;
    }
    ensureInsightPanel(cardEl, slot);
  }
};
