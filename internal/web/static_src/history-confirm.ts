import { confirmAction } from "./action-confirm";
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

export const confirmUndo = (
  preview: HistoryPreviewResult, doc = document, returnFocus: Element | null = doc.activeElement,
): Promise<boolean> => confirmAction({
  title: `${undoActionLabel(preview)}?`,
  description: undoDescription(preview),
  confirmLabel: preview.requires_force ? "Restore and replace newer work" : undoActionLabel(preview),
}, doc, returnFocus);

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
