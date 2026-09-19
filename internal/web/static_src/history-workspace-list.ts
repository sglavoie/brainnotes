import type { HistoryEntry, HistoryResult, HistoryWarning } from "./api";
import { formatHistoryTimestamp, historyFileCountLabel } from "./history-panel-view";
import type { HistoryWorkspaceElements } from "./history-workspace-elements";

/**
 * renderHistoryWarnings shows the entries the journal could not read.
 *
 * They are never filtered and never paged: an unreadable manifest has no
 * command and no paths, so no filter can be true of it, and a page that dropped
 * it would be a complete-looking account of an incomplete history. The count is
 * the server's own total rather than the length of this list, because the list
 * is capped and the total is not.
 */
export const renderHistoryWarnings = (
  container: HTMLElement,
  warnings: HistoryWarning[],
  warningCount: number
): void => {
  container.textContent = "";
  container.hidden = warningCount === 0;
  if (warningCount === 0) {
    return;
  }
  for (const warning of warnings) {
    const item = document.createElement("li");
    item.className = "history-warning";
    item.textContent = warning.id
      ? `${warning.id}: ${warning.message}`
      : warning.message;
    container.appendChild(item);
  }
  if (warningCount > warnings.length) {
    const more = document.createElement("li");
    more.className = "history-warning";
    more.textContent = `${warningCount - warnings.length} more unreadable entries are not listed here.`;
    container.appendChild(more);
  }
};

const historyClassSummary = (entry: HistoryEntry): string =>
  entry.classes
    .map((count) => `${count.label || count.class} ${count.count}`)
    .join(", ");

const historyEntryCard = (
  entry: HistoryEntry,
  onRestore: (entryID: string) => void
): HTMLLIElement => {
  const item = document.createElement("li");
  item.className = "history-entry";
  item.setAttribute("data-history-entry", entry.id);

  const head = document.createElement("div");
  head.className = "history-entry-head";

  const origin = document.createElement("span");
  origin.className = "history-origin";
  origin.setAttribute("data-history-entry-origin", entry.origin);
  origin.textContent = entry.origin_label || entry.origin;
  head.appendChild(origin);

  // The classified origin is a reading of the recorded command, so the command
  // itself stays on the row: somebody who does not believe the label can check
  // it without leaving the page.
  const command = document.createElement("code");
  command.className = "history-command";
  command.textContent = entry.command || entry.id;
  head.appendChild(command);

  const meta = document.createElement("p");
  meta.className = "history-meta";
  const classes = historyClassSummary(entry);
  const parts = [
    formatHistoryTimestamp(entry.timestamp),
    historyFileCountLabel(entry.file_count || entry.files.length),
  ];
  meta.textContent = classes ? `${parts.join(" - ")} - ${classes}` : parts.join(" - ");

  const restore = document.createElement("button");
  restore.className = "history-button";
  restore.type = "button";
  restore.setAttribute("data-history-preview-entry", entry.id);
  restore.setAttribute("aria-label", `Preview restoring ${entry.command || entry.id}`);
  restore.textContent = "Restore...";
  restore.addEventListener("click", () => {
    onRestore(entry.id);
  });

  item.appendChild(head);
  item.appendChild(meta);
  item.appendChild(restore);
  return item;
};

export const renderHistoryEntryList = (
  container: HTMLElement,
  entries: HistoryEntry[],
  onRestore: (entryID: string) => void
): void => {
  container.textContent = "";
  for (const entry of entries) {
    container.appendChild(historyEntryCard(entry, onRestore));
  }
};

/** historyRangeLabel names the window, not just the page, so a filtered view
 * cannot be mistaken for the whole journal. */
export const historyRangeLabel = (offset: number, shown: number, total: number): string => {
  if (total === 0) {
    return "No entries match.";
  }
  if (shown === 0) {
    return `No entries at this position of ${total}.`;
  }
  return `${offset + 1}-${offset + shown} of ${total}`;
};

export const updateHistoryPager = (
  elements: HistoryWorkspaceElements,
  result: HistoryResult,
  shown: number
): void => {
  const offset = result.offset || 0;
  const total = result.total || 0;
  if (elements.range) {
    elements.range.textContent = historyRangeLabel(offset, shown, total);
  }
  if (elements.previous) {
    elements.previous.disabled = offset <= 0;
  }
  if (elements.next) {
    elements.next.disabled = !result.has_more;
  }
};
