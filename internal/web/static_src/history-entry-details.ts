import type { HistoryEntry } from "./api";

export const historyEntryDetails = (entry: HistoryEntry, doc = document): HTMLDetailsElement => {
  const details = doc.createElement("details"), label = doc.createElement("summary");
  details.className = "history-entry-details";
  label.textContent = "Details";
  const command = doc.createElement("code");
  command.className = "history-command";
  command.textContent = entry.command || "No command recorded";
  const id = doc.createElement("p");
  id.textContent = `Entry: ${entry.id}`;
  details.append(label, command, id);
  return details;
};
