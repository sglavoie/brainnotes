import {
  buildBriefInsightForm,
  buildBriefItemForm,
  buildBriefViewForm,
  normalizeBriefInsightResponse,
  normalizeBriefItemResponse,
  normalizeBriefViewResponse,
  normalizeManagementErrorMessage,
  type BriefItemResult,
  type BriefRollingActivity,
  type BriefSlotKind,
} from "./api";
import { jsonPostRequest, readResponsePayload } from "./daily-brief-http";
import {
  insightDisclosureMessage,
  normalizeSlot,
  slotLabel,
  type ClipboardWriter,
  type ConfirmLike,
  type DailyBriefConfig,
  type FetchLike,
} from "./daily-brief-model";
import { findSlotElement, renderInsight } from "./daily-brief-render";
import { queryElement } from "./dom";
import { type StatusKind } from "./status";

// BriefActionContext is the mutable state and collaborator set the brief
// actions share. insightCounter and insightDisclosureAccepted are written back
// onto the context so every handler observes the same values.
export type BriefActionContext = {
  root: HTMLElement;
  config: DailyBriefConfig;
  fetcher: FetchLike;
  confirmer: ConfirmLike;
  clipboardWrite: ClipboardWriter | undefined;
  openedCards: Set<string>;
  inflightInsight: Map<BriefSlotKind, number>;
  insightCounter: number;
  insightDisclosureAccepted: boolean;
  setGlobalStatus: (message: string, kind?: StatusKind) => void;
  setSlotStatus: (slot: BriefSlotKind, message: string, kind?: StatusKind) => void;
  setSlotsBusy: (slots: BriefSlotKind[], busy: boolean) => void;
  updateActivity: (activity: BriefRollingActivity) => void;
  markCardOpened: (slot: BriefSlotKind, cardID: string) => void;
  applyAnotherPayload: (result: BriefItemResult, requestedSlot: BriefSlotKind) => BriefSlotKind[];
};

const readButtonSlot = (button: HTMLButtonElement): BriefSlotKind | "" =>
  normalizeSlot(button.getAttribute("data-brief-slot") || "");

const readButtonCardID = (button: HTMLButtonElement): string =>
  button.getAttribute("data-brief-card-id") || "";

export const runOpen = async (ctx: BriefActionContext, button: HTMLButtonElement): Promise<void> => {
  const slot = readButtonSlot(button);
  const cardID = readButtonCardID(button);
  if (!slot || !cardID || !ctx.config.csrfToken) {
    ctx.setGlobalStatus("Open action unavailable.", "is-error");
    return;
  }

  ctx.setSlotsBusy([slot], true);
  ctx.setSlotStatus(slot, "Opening editor...", "is-loading");
  try {
    const body = buildBriefItemForm({ cardID, action: "open", token: ctx.config.csrfToken }).toString();
    const resp = await ctx.fetcher(ctx.config.itemEndpoint, jsonPostRequest(ctx.config.csrfToken, body));
    const payload = await readResponsePayload(resp);
    if (!resp.ok) {
      ctx.setSlotStatus(slot, normalizeManagementErrorMessage(resp.status, payload), "is-error");
      return;
    }
    const result = normalizeBriefItemResponse(payload);
    if (!result.ok) {
      ctx.setSlotStatus(slot, result.message || "Open failed.", "is-error");
      return;
    }
    ctx.updateActivity(result.activity);
    ctx.openedCards.add(cardID);
    ctx.markCardOpened(slot, cardID);
    ctx.setSlotStatus(slot, "Opened in editor. Refresh after saving changes.", "is-success");
    ctx.setGlobalStatus("Brief activity updated.", "is-success");
  } catch {
    ctx.setSlotStatus(slot, "Open failed.", "is-error");
  } finally {
    ctx.setSlotsBusy([slot], false);
  }
};

export const runAnother = async (
  ctx: BriefActionContext,
  button: HTMLButtonElement
): Promise<void> => {
  const slot = readButtonSlot(button);
  const cardID = readButtonCardID(button);
  if (!slot || !cardID || !ctx.config.csrfToken) {
    ctx.setGlobalStatus("Another action unavailable.", "is-error");
    return;
  }
  const busySlots: BriefSlotKind[] = slot === "continue" ? ["continue", "connect"] : [slot];

  ctx.setSlotsBusy(busySlots, true);
  ctx.setSlotStatus(slot, "Finding another card...", "is-loading");
  try {
    const body = buildBriefItemForm({ cardID, action: "another", token: ctx.config.csrfToken }).toString();
    const resp = await ctx.fetcher(ctx.config.itemEndpoint, jsonPostRequest(ctx.config.csrfToken, body));
    const payload = await readResponsePayload(resp);
    if (!resp.ok) {
      ctx.setSlotStatus(slot, normalizeManagementErrorMessage(resp.status, payload), "is-error");
      return;
    }
    const result = normalizeBriefItemResponse(payload);
    if (!result.ok) {
      ctx.setSlotStatus(slot, result.message || "Another failed.", "is-error");
      return;
    }

    ctx.updateActivity(result.activity);
    const changedSlots = ctx.applyAnotherPayload(result, slot);
    const statusMessage =
      result.message ||
      (slot === "continue" && changedSlots.includes("connect")
        ? "Updated Continue and Connect."
        : `Updated ${slotLabel(slot)}.`);
    ctx.setSlotStatus(slot, statusMessage, "is-success");
    ctx.setGlobalStatus(statusMessage, "is-success");

    const slotEl = findSlotElement(ctx.root, slot);
    const nextFocus =
      (slotEl && queryElement(slotEl, "[data-brief-action-another]", HTMLButtonElement)) ||
      (slotEl && queryElement(slotEl, "[data-brief-action]", HTMLButtonElement));
    nextFocus?.focus({ preventScroll: true });
  } catch {
    ctx.setSlotStatus(slot, "Another failed.", "is-error");
  } finally {
    ctx.setSlotsBusy(busySlots, false);
  }
};

export const runInsight = async (
  ctx: BriefActionContext,
  button: HTMLButtonElement
): Promise<void> => {
  const slot = readButtonSlot(button);
  const cardID = readButtonCardID(button);
  if (!slot || !cardID || !ctx.config.csrfToken) {
    ctx.setGlobalStatus("Ask AI is unavailable.", "is-error");
    return;
  }
  const cardEl = button.closest<HTMLElement>("[data-brief-card]");
  const prompt = cardEl
    ? queryElement(cardEl, "[data-brief-insight-prompt]", HTMLTextAreaElement)?.value.trim() || ""
    : "";
  if (!ctx.insightDisclosureAccepted) {
    ctx.insightDisclosureAccepted = ctx.confirmer(insightDisclosureMessage);
    if (!ctx.insightDisclosureAccepted) {
      ctx.setSlotStatus(slot, "Ask AI canceled.", "is-empty");
      return;
    }
  }

  ctx.setSlotsBusy([slot], true);
  ctx.setSlotStatus(slot, "Generating insight...", "is-loading");
  const requestID = ctx.insightCounter + 1;
  ctx.insightCounter = requestID;
  ctx.inflightInsight.set(slot, requestID);

  try {
    const body = buildBriefInsightForm({ cardID, prompt, token: ctx.config.csrfToken }).toString();
    const resp = await ctx.fetcher(
      ctx.config.insightEndpoint,
      jsonPostRequest(ctx.config.csrfToken, body)
    );
    const payload = await readResponsePayload(resp);
    if (ctx.inflightInsight.get(slot) !== requestID) {
      return;
    }
    if (!resp.ok) {
      ctx.setSlotStatus(slot, normalizeManagementErrorMessage(resp.status, payload), "is-error");
      return;
    }

    const result = normalizeBriefInsightResponse(payload);
    if (!result.ok) {
      ctx.setSlotStatus(slot, result.message || "Ask AI failed.", "is-error");
      return;
    }
    if (!result.enabled) {
      ctx.setSlotStatus(slot, result.message || "Ask AI is unavailable.", "is-error");
      return;
    }
    if (result.card_id && result.card_id !== cardID) {
      ctx.setSlotStatus(slot, "Card changed while insight was running. Try again.", "is-error");
      return;
    }

    renderInsight(ctx.root, slot, cardID, result.rendered_html, result.output);
    ctx.setSlotStatus(slot, "Insight ready.", "is-success");
  } catch {
    if (ctx.inflightInsight.get(slot) === requestID) {
      ctx.setSlotStatus(slot, "Ask AI failed.", "is-error");
    }
  } finally {
    if (ctx.inflightInsight.get(slot) === requestID) {
      ctx.inflightInsight.delete(slot);
      ctx.setSlotsBusy([slot], false);
    }
  }
};

export const runCopyInsight = async (
  ctx: BriefActionContext,
  button: HTMLButtonElement
): Promise<void> => {
  const slot = normalizeSlot(button.getAttribute("data-brief-slot") || "");
  if (!slot) {
    return;
  }
  const payload = button.getAttribute("data-brief-copy-output") || "";
  if (payload.trim() === "") {
    ctx.setSlotStatus(slot, "Nothing to copy.", "is-error");
    return;
  }
  if (!ctx.clipboardWrite) {
    ctx.setSlotStatus(slot, "Clipboard unavailable.", "is-error");
    return;
  }
  button.disabled = true;
  try {
    await ctx.clipboardWrite(payload);
    ctx.setSlotStatus(slot, "Insight markdown copied.", "is-success");
  } catch {
    ctx.setSlotStatus(slot, "Copy failed.", "is-error");
  } finally {
    button.disabled = false;
  }
};

export const recordView = async (ctx: BriefActionContext): Promise<void> => {
  if (!ctx.config.csrfToken) {
    return;
  }
  try {
    const body = buildBriefViewForm({ token: ctx.config.csrfToken }).toString();
    const resp = await ctx.fetcher(ctx.config.viewEndpoint, jsonPostRequest(ctx.config.csrfToken, body));
    const payload = await readResponsePayload(resp);
    if (!resp.ok) {
      ctx.setGlobalStatus(normalizeManagementErrorMessage(resp.status, payload), "is-error");
      return;
    }
    const result = normalizeBriefViewResponse(payload);
    if (!result.ok) {
      ctx.setGlobalStatus(result.message || "Brief activity unavailable.", "is-error");
      return;
    }
    ctx.updateActivity(result.activity);
  } catch {
    ctx.setGlobalStatus("Brief activity unavailable.", "is-error");
  }
};
