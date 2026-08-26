import type { SavedViewsResult } from "./api-types";
import {
  savedViewCreateReceipt,
  savedViewDefinition,
  savedViewNoChangeMessage,
  savedViewOverwriteMessage,
  savedViewPendingSave,
  savedViewSaveError,
  savedViewSavedMessage,
  type FetchLike,
  type SavedViewCreateReceipt,
  type SavedViewPendingSave,
  type SavedViewSaveInput,
} from "./saved-views-model";
import { applySavedView, previewSavedView } from "./saved-views-requests";
import type { StatusKind } from "./status";

export type SavedViewSaveHooks = {
  /** Reports progress, refusals and the final outcome. */
  onStatus?: (message: string, kind: StatusKind) => void;
  /**
   * Reports the receipt an overwrite is waiting on, or null once nothing is
   * pending. A create never reports one: it is applied before the user could
   * act on it, so its diff arrives through `onReceipt` instead.
   */
  onPending?: (pending: SavedViewPendingSave | null) => void;
  /**
   * Reports the diff a create just wrote, or null once there is nothing to
   * show. This is a post-success record, never a question: it fires only after
   * the apply succeeded and carries no preview ID, so it cannot be acted on.
   */
  onReceipt?: (receipt: SavedViewCreateReceipt | null) => void;
  /** Fires once the write landed, carrying the reloaded view list. */
  onSaved?: (result: SavedViewsResult) => void;
};

export type SavedViewSaver = {
  save: (input: SavedViewSaveInput) => Promise<void>;
  confirm: () => Promise<void>;
  cancel: () => void;
  pending: () => SavedViewPendingSave | null;
  receipt: () => SavedViewCreateReceipt | null;
  busy: () => boolean;
};

export type SavedViewSaverDeps = {
  fetch: FetchLike;
  token: string;
  hooks?: SavedViewSaveHooks;
};

/**
 * Drives one saved-view save through preview and apply.
 *
 * The branch is the whole point. `PatchViews` rewrites only the `name` line on
 * a rename but re-emits the whole block on an upsert, so an overwrite destroys
 * any comment inside the block it replaces while a create destroys nothing.
 * That asymmetry is why a **create chains preview straight into apply** — there
 * is no existing text to lose, so a confirmation would be a click that asks
 * nothing — while an **overwrite stops on the rendered diff** and writes only
 * after an explicit confirmation.
 *
 * Both paths still go through preview: the receipt is what pins the config the
 * apply is allowed to write over, so the auto-chain is a skipped *question*,
 * never a skipped preview.
 *
 * A skipped question is not a discarded diff. Once a create's apply lands, its
 * previewed diff is published through `onReceipt` as a post-success record of
 * what was written — the same rendered diff an overwrite confirms, shown after
 * the fact rather than before, and stripped of the consumed preview ID so it
 * can never be applied a second time.
 */
export const createSavedViewSaver = (deps: SavedViewSaverDeps): SavedViewSaver => {
  const hooks = deps.hooks || {};
  let pending: SavedViewPendingSave | null = null;
  let receipt: SavedViewCreateReceipt | null = null;
  let busy = false;

  const setStatus = (message: string, kind: StatusKind = ""): void => {
    hooks.onStatus?.(message, kind);
  };

  const setPending = (next: SavedViewPendingSave | null): void => {
    pending = next;
    hooks.onPending?.(next);
  };

  // Clearing an already-clear receipt reports nothing, so a save that never
  // produced one does not push an empty render at the UI.
  const setReceipt = (next: SavedViewCreateReceipt | null): void => {
    if (receipt === null && next === null) {
      return;
    }
    receipt = next;
    hooks.onReceipt?.(next);
  };

  // An issued preview is one-use, so every apply outcome — success or failure —
  // drops it. A retry has to re-preview, which is what keeps a second apply from
  // being attempted against a receipt the server already consumed.
  const runApply = async (issued: SavedViewPendingSave): Promise<void> => {
    setStatus(`Saving view "${issued.name}"...`, "is-loading");
    const outcome = await applySavedView(deps.fetch, deps.token, issued.previewID);
    setPending(null);
    if (!outcome.ok) {
      setStatus(outcome.message, "is-error");
      return;
    }
    setStatus(savedViewSavedMessage(issued.name, issued.created), "is-success");
    // The create receipt is published only here, after the write landed, and
    // only for a create: an overwrite already showed its diff as the question
    // it was confirming.
    if (issued.created) {
      setReceipt(savedViewCreateReceipt(issued));
    }
    hooks.onSaved?.(outcome.result);
  };

  const save = async (input: SavedViewSaveInput): Promise<void> => {
    if (busy) {
      return;
    }
    const refusal = savedViewSaveError(input);
    if (refusal !== "") {
      setPending(null);
      setReceipt(null);
      setStatus(refusal, "is-error");
      return;
    }
    if (deps.token.trim() === "") {
      setPending(null);
      setReceipt(null);
      setStatus("Saving views is unavailable.", "is-error");
      return;
    }

    busy = true;
    setPending(null);
    // A new save supersedes the previous create's receipt, so the block never
    // shows a diff from a write two saves ago.
    setReceipt(null);
    setStatus(`Previewing view "${input.name}"...`, "is-loading");
    try {
      const outcome = await previewSavedView(deps.fetch, deps.token, {
        token: deps.token,
        op: "upsert",
        name: input.name,
        view: savedViewDefinition(input),
      });
      if (!outcome.ok) {
        setStatus(outcome.message, "is-error");
        return;
      }
      // No receipt means the patch changed nothing: the view is already stored
      // exactly like this, so there is neither a diff to confirm nor a write to
      // chain into.
      if (outcome.result.preview_id === "") {
        setStatus(savedViewNoChangeMessage(input.name), "");
        return;
      }

      const receipt = savedViewPendingSave(input.name, outcome.result);
      if (receipt.created) {
        await runApply(receipt);
        return;
      }
      setPending(receipt);
      setStatus(savedViewOverwriteMessage(input.name), "is-warning");
    } finally {
      busy = false;
    }
  };

  const confirm = async (): Promise<void> => {
    if (busy) {
      return;
    }
    // A create leaves `pending` null, so a confirmation aimed at a create's
    // receipt finds nothing to apply and is refused like any other unbacked
    // confirmation.
    const issued = pending;
    if (!issued) {
      setStatus("Preview the view before saving it.", "is-error");
      return;
    }
    busy = true;
    try {
      await runApply(issued);
    } finally {
      busy = false;
    }
  };

  const cancel = (): void => {
    setReceipt(null);
    if (pending === null) {
      return;
    }
    setPending(null);
    setStatus("Overwrite cancelled.", "");
  };

  return {
    save,
    confirm,
    cancel,
    pending: () => pending,
    receipt: () => receipt,
    busy: () => busy,
  };
};
