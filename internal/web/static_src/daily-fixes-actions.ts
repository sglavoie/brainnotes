import { responseFailure, unansweredRequest } from "./request-outcome";
import {
  buildOpenNoteForm,
  managementMutationEventName,
  normalizeCockpitQueueResponse,
  normalizeFixApplyResponse,
  normalizeFixPreviewResponse,
  normalizeManagementErrorMessage,
  normalizeOpenErrorMessage,
  noteNavigationDestination,
  type CockpitItemAction,
  type CockpitQueueResult,
  type CockpitSnooze,
  type FixAction,
} from "./api";
import {
  fixCountsStatusMessage,
  itemActionLoadingMessage,
  normalizeFixAction,
  normalizeItemAction,
  normalizeSnooze,
  type DailyFixesConfig,
  type FetchLike,
  type PreviewState,
} from "./daily-fixes-model";
import { renderPreview } from "./daily-fixes-preview";
import {
  buildFixesApplyFetchRequest,
  buildFixesItemFetchRequest,
  buildFixesPreviewFetchRequest,
  jsonPostRequest,
  readResponsePayload,
} from "./daily-fixes-requests";
import { type StatusKind } from "./status";

// DailyFixesContext is what a queue action needs from the panel it lives in.
// The runners moved out of initDailyFixes so each one is a named function of its
// own rather than a closure inside a single 300-line arrow — the panel keeps the
// element wiring and the mutable state, and hands both over here.
export type DailyFixesContext = {
  config: DailyFixesConfig;
  fetcher: FetchLike;
  listEl: HTMLElement;
  previews: Map<string, PreviewState>;
  setBusy: (next: boolean) => void;
  setStatus: (message: string, kind?: StatusKind) => void;
  setError: (message: string) => void;
  showError: (message: string) => void;
  setQueueStale: (stale: boolean) => void;
  renderResult: (result: CockpitQueueResult) => void;
  clearPreview: (id: string) => void;
  // loadQueue is in the context rather than imported directly because applying a
  // fix reloads the queue, and the panel owns the "which message does the reload
  // report" decision.
  loadQueue: (successMessage?: string) => Promise<void>;
  // suppressNextMutation tells the panel that the mutation event it is about to
  // see is its own, so it does not reload a queue it already rendered.
  suppressNextMutation: () => void;
};

type ItemActionRequest = {
  id: string;
  action: CockpitItemAction;
  snooze?: CockpitSnooze;
};

type PreviewRequest = {
  id: string;
  action: FixAction;
};

// readItemActionRequest validates a button before anything is sent. Pulling the
// validation out keeps each runner's own branching down to the request itself.
const readItemActionRequest = (
  button: HTMLButtonElement,
  csrfToken: string
): ItemActionRequest | null => {
  const id = button.getAttribute("data-fixes-id") || "";
  const action = normalizeItemAction(button.getAttribute("data-fixes-item-action") || "");
  const snooze = normalizeSnooze(button.getAttribute("data-fixes-snooze") || "");
  if (!id || !action || !csrfToken) {
    return null;
  }
  if (action === "snooze" && !snooze) {
    return null;
  }
  return { id, action, snooze };
};

const readPreviewRequest = (
  button: HTMLButtonElement,
  csrfToken: string
): PreviewRequest | null => {
  const id = button.getAttribute("data-fixes-id") || "";
  const action = normalizeFixAction(button.getAttribute("data-fixes-fix-action") || "");
  if (!id || !action || !csrfToken) {
    return null;
  }
  return { id, action };
};

export const applyQueueResult = (
  ctx: DailyFixesContext,
  payload: unknown,
  successMessage = "",
  successKind: StatusKind = ""
): boolean => {
  const result = normalizeCockpitQueueResponse(payload);
  if (!result.ok) {
    ctx.showError(result.message || "Cockpit request failed.");
    return false;
  }
  ctx.setError("");
  ctx.renderResult(result);
  ctx.setQueueStale(false);
  const message = successMessage || fixCountsStatusMessage(result.counts);
  const kind = successKind || (result.counts.active === 0 ? "is-empty" : "");
  ctx.setStatus(message, kind);
  return true;
};

export const loadQueue = async (ctx: DailyFixesContext, successMessage = ""): Promise<void> => {
  ctx.setBusy(true);
  ctx.setError("");
  ctx.setQueueStale(true);
  ctx.setStatus("Loading fixes queue…", "is-loading");
  try {
    const resp = await ctx.fetcher(ctx.config.queueEndpoint, {
      headers: { Accept: "application/json" },
    });
    const payload = await readResponsePayload(resp);
    if (!resp.ok) {
      ctx.showError(normalizeManagementErrorMessage(resp.status, payload));
      return;
    }
    applyQueueResult(ctx, payload, successMessage, successMessage ? "is-success" : "");
  } catch {
    ctx.showError("Fixes queue unavailable.");
  } finally {
    ctx.setBusy(false);
  }
};

export const runItemAction = async (
  ctx: DailyFixesContext,
  button: HTMLButtonElement
): Promise<void> => {
  const request = readItemActionRequest(button, ctx.config.csrfToken);
  if (!request) {
    ctx.showError("Fix action unavailable.");
    return;
  }

  ctx.setBusy(true);
  ctx.setStatus(itemActionLoadingMessage(request.action, request.snooze), "is-loading");
  try {
    const fetchRequest = buildFixesItemFetchRequest(
      ctx.config.itemEndpoint,
      ctx.config.csrfToken,
      request.id,
      request.action,
      request.snooze
    );
    const resp = await ctx.fetcher(fetchRequest.url, fetchRequest.init);
    const payload = await readResponsePayload(resp);
    if (!resp.ok) {
      ctx.showError(normalizeManagementErrorMessage(resp.status, payload));
      return;
    }
    const result = normalizeCockpitQueueResponse(payload);
    announceItemMutation(ctx, request.action, payload);
    applyQueueResult(ctx, payload, result.message || "Fix updated.", "is-success");
  } catch {
    ctx.showError("Fix action failed.");
  } finally {
    ctx.setBusy(false);
  }
};

// announceItemMutation republishes the two item actions that change notes, so
// every other panel on the page refreshes. The rest only move queue state.
const announceItemMutation = (
  ctx: DailyFixesContext,
  action: CockpitItemAction,
  payload: unknown
): void => {
  if (action !== "exempt_dead_end" && action !== "clean_long_tail") {
    return;
  }
  ctx.suppressNextMutation();
  window.dispatchEvent(new CustomEvent(managementMutationEventName, { detail: payload }));
};

export const runPreview = async (
  ctx: DailyFixesContext,
  button: HTMLButtonElement
): Promise<void> => {
  const request = readPreviewRequest(button, ctx.config.csrfToken);
  if (!request) {
    ctx.showError("Fix preview unavailable.");
    return;
  }

  ctx.setBusy(true);
  ctx.setStatus("Previewing fix…", "is-loading");
  try {
    const fetchRequest = buildFixesPreviewFetchRequest(
      ctx.config.previewEndpoint,
      ctx.config.csrfToken,
      request.id,
      request.action
    );
    const resp = await ctx.fetcher(fetchRequest.url, fetchRequest.init);
    const payload = await readResponsePayload(resp);
    if (!resp.ok) {
      ctx.showError(normalizeManagementErrorMessage(resp.status, payload));
      return;
    }
    recordPreview(ctx, request, payload);
  } catch {
    ctx.showError("Fix preview failed.");
  } finally {
    ctx.setBusy(false);
  }
};

const recordPreview = (
  ctx: DailyFixesContext,
  request: PreviewRequest,
  payload: unknown
): void => {
  const result = normalizeFixPreviewResponse(payload);
  if (!result.ok || !result.preview_id) {
    ctx.showError(result.message || "Fix preview failed.");
    return;
  }
  ctx.previews.set(request.id, { action: request.action, previewID: result.preview_id });
  const message = result.message || "Preview ready.";
  renderPreview(ctx.listEl, request.id, request.action, result.previews, message);
  ctx.setError("");
  ctx.setStatus(message, "is-success");
};

export const runApply = async (
  ctx: DailyFixesContext,
  button: HTMLButtonElement
): Promise<void> => {
  const id = button.getAttribute("data-fixes-id") || "";
  const state = ctx.previews.get(id);
  if (!id || !state || !ctx.config.csrfToken) {
    ctx.showError("Preview the fix before applying it.");
    return;
  }

  ctx.setBusy(true);
  ctx.setStatus("Applying fix…", "is-loading");
  try {
    const request = buildFixesApplyFetchRequest(
      ctx.config.applyEndpoint,
      ctx.config.csrfToken,
      id,
      state.action,
      state.previewID
    );
    const resp = await ctx.fetcher(request.url, request.init);
    await completeApply(ctx, id, resp, await readResponsePayload(resp));
  } catch {
    ctx.clearPreview(id);
    ctx.showError(unansweredRequest("write").message);
  } finally {
    ctx.setBusy(false);
  }
};

// completeApply decides what an answered apply meant. It is a function of its
// own so runApply stays a request and a failure envelope: the three outcomes
// here — a refusal the server described, a refusal it did not, and a success
// that every other panel needs to hear about — are one decision, not three
// branches sharing a try block.
const completeApply = async (
  ctx: DailyFixesContext,
  id: string,
  resp: { ok: boolean; status: number; redirected?: boolean },
  payload: unknown
): Promise<void> => {
  const failure = responseFailure(resp, payload, "write");
  if (failure?.uncertain) {
    ctx.clearPreview(id);
    ctx.showError(failure.message);
    return;
  }
  const result = resp.ok ? normalizeFixApplyResponse(payload) : null;
  if (!result?.ok) {
    ctx.clearPreview(id);
    ctx.showError(
      result
        ? result.message || "Fix apply failed."
        : normalizeManagementErrorMessage(resp.status, payload)
    );
    await ctx.loadQueue();
    return;
  }
  ctx.previews.delete(id);
  ctx.suppressNextMutation();
  window.dispatchEvent(new CustomEvent(managementMutationEventName, { detail: result }));
  await ctx.loadQueue(result.message || "Fix applied.");
};

// runOpen is the fix queue's Open action. A server that cannot start an editor
// answers with the route to navigate to instead, so the queue stays useful in
// hosted mode: the point of an Open here is reaching the offending note, and
// that is a different note from the page the reader is on.
export const runOpen = async (
  ctx: DailyFixesContext,
  button: HTMLButtonElement
): Promise<void> => {
  const key = button.getAttribute("data-fixes-open-key") || "";
  if (!key || !ctx.config.csrfToken) {
    ctx.showError("Open action unavailable.");
    return;
  }
  ctx.setBusy(true);
  ctx.setStatus("Opening note…", "is-loading");
  try {
    const body = buildOpenNoteForm({ key, token: ctx.config.csrfToken });
    const request = jsonPostRequest(ctx.config.openEndpoint, ctx.config.csrfToken, body.toString());
    const resp = await ctx.fetcher(request.url, request.init);
    const payload = await readResponsePayload(resp);
    if (!resp.ok) {
      ctx.showError(normalizeOpenErrorMessage(resp.status, payload));
      return;
    }
    ctx.setError("");
    const destination = noteNavigationDestination(payload);
    if (destination) {
      window.location.assign(destination);
      return;
    }
    ctx.setStatus("Opened in editor. Refresh after saving changes.", "is-success");
  } catch {
    ctx.showError("Open failed.");
  } finally {
    ctx.setBusy(false);
  }
};

// fixesClickHandlers is the click routing table. It replaces a chain of
// closest()/contains() pairs so adding an action is a row rather than another
// branch in one function the ratchet already judges as too complex.
const fixesClickHandlers: Array<{
  selector: string;
  run: (ctx: DailyFixesContext, button: HTMLButtonElement) => void;
}> = [
  { selector: "[data-fixes-reload]", run: (ctx) => void ctx.loadQueue("Fixes refreshed.") },
  { selector: "[data-fixes-fix-action]", run: (ctx, button) => void runPreview(ctx, button) },
  { selector: "[data-fixes-preview-apply]", run: (ctx, button) => void runApply(ctx, button) },
  {
    selector: "[data-fixes-preview-cancel]",
    run: (ctx, button) => ctx.clearPreview(button.getAttribute("data-fixes-id") || ""),
  },
  { selector: "[data-fixes-item-action]", run: (ctx, button) => void runItemAction(ctx, button) },
  { selector: "[data-fixes-open-key]", run: (ctx, button) => void runOpen(ctx, button) },
];

export const dispatchFixesClick = (
  ctx: DailyFixesContext,
  root: HTMLElement,
  target: EventTarget | null
): void => {
  if (!(target instanceof Element)) {
    return;
  }
  for (const handler of fixesClickHandlers) {
    const button = target.closest<HTMLButtonElement>(handler.selector);
    if (button && root.contains(button)) {
      handler.run(ctx, button);
      return;
    }
  }
};
