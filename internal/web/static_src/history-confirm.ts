import type { HistoryPreviewResult, UndoRequest } from "./api-types";
import { HISTORY_PREVIEW_ENDPOINT } from "./api-history";
import { previewHistoryRestore, type HistoryFetch } from "./history-requests";
import { formatHistoryTimestamp } from "./history-panel-view";

export const undoActionLabel = (preview: HistoryPreviewResult): string => {
  const action = preview.command?.match(/^bn (?:web |serve )?(?:notes )?(rename|move|trash|capture|save|frontmatter)\b/)?.[1];
  if (action === "trash") return "Restore note";
  if (action === "frontmatter") return "Undo property change";
  return action ? `Undo ${action}` : "Undo change";
};

const restoreEffect = (current: string, restored: string): string => {
  if (restored === "missing") return "remove the created path";
  return current === "missing" ? "restore the removed path" : "restore the earlier version";
};

export const undoDescription = (preview: HistoryPreviewResult): string => [
  preview.command || "Recorded change",
  formatHistoryTimestamp(preview.timestamp || ""),
  ...(preview.changes || []).map((change) =>
    `${change.path}: ${restoreEffect(change.current_state, change.restored_state)}${change.conflict ? " (replaces newer work)" : ""}`),
].join("\n");

// A native dialog supplies keyboard containment and Escape. Text is assigned
// through textContent because paths and commands are vault-authored content.
export const confirmUndo = (
  preview: HistoryPreviewResult, doc = document, returnFocus: Element | null = doc.activeElement,
): Promise<boolean> =>
  new Promise((resolve) => {
    const dialog = doc.createElement("dialog");
    dialog.className = "properties-dialog history-confirm";
    dialog.setAttribute("aria-labelledby", "history-confirm-heading");
    const heading = doc.createElement("h2");
    heading.id = "history-confirm-heading";
    heading.textContent = `${undoActionLabel(preview)}?`;
    const description = doc.createElement("pre");
    description.className = "history-confirm-description";
    description.textContent = undoDescription(preview);
    const actions = doc.createElement("div");
    actions.className = "history-actions";
    const cancel = doc.createElement("button");
    cancel.className = "history-button";
    cancel.textContent = "Cancel";
    cancel.autofocus = true;
    const confirm = doc.createElement("button");
    confirm.className = "history-button history-danger";
    confirm.textContent = preview.requires_force ? "Restore and replace newer work" : undoActionLabel(preview);
    cancel.addEventListener("click", () => dialog.close("cancel"));
    confirm.addEventListener("click", () => dialog.close("confirm"));
    dialog.addEventListener("close", () => {
      const accepted = dialog.returnValue === "confirm";
      dialog.remove();
      resolve(accepted);
      // Callers re-enable controls when their awaited confirmation settles.
      doc.defaultView?.setTimeout(() => {
        if (doc.activeElement === doc.body && returnFocus instanceof HTMLElement && returnFocus.isConnected) returnFocus.focus();
      }, 0);
    }, { once: true });
    actions.append(cancel, confirm);
    dialog.append(heading, description, actions);
    doc.body.append(dialog);
    dialog.showModal();
  });

// The identity and receipt both come from the preview. A new journal entry
// appearing while the dialog is open cannot retarget this confirmation.
export const prepareExactUndo = async (
  fetcher: HistoryFetch, token: string, entryID = "",
  confirm: (preview: HistoryPreviewResult) => boolean | Promise<boolean> = confirmUndo,
  force = false,
): Promise<UndoRequest | string> => {
  const outcome = await previewHistoryRestore(fetcher, HISTORY_PREVIEW_ENDPOINT, token, entryID);
  if (!outcome.ok) return `Couldn't preview undo. ${outcome.message} No change was submitted.`;
  const preview = outcome.result;
  const refusal = undoPreviewRefusal(preview, force);
  if (refusal) return refusal;
  if (!await confirm(preview)) return "Undo canceled. Nothing was changed.";
  return { token, entryID: preview.entry_id, previewID: preview.preview_id,
    ...(force ? { force: true, confirmForce: true } : {}) };
};

const undoPreviewRefusal = (preview: HistoryPreviewResult, force: boolean): string => {
  if (!preview.entry_id || !preview.preview_id) return "Couldn't identify the change to undo. No change was submitted.";
  if (preview.requires_force && !force) {
    const paths = (preview.conflicts || []).map((conflict) => conflict.path).join(", ");
    return `This undo would replace newer work in ${paths}. Open History to review the affected paths. No change was submitted.`;
  }
  return "";
};
