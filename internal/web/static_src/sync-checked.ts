import { formatSyncTimestamp } from "./sync-status-view";

// Age is measured from the server's observation, never from the browser fetch.
export const syncCheckedText = (observed: string, now = Date.now()): string => {
  const at = Date.parse(observed);
  if (!Number.isFinite(at)) return observed ? `Checked ${observed}.` : "";
  const minutes = Math.floor((now - at) / 60_000);
  if (minutes < 0) return `Checked ${formatSyncTimestamp(observed)}.`;
  if (minutes === 0) return "Checked just now.";
  const hours = Math.floor(minutes / 60);
  if (hours === 0) return `Checked ${minutes} minute${minutes === 1 ? "" : "s"} ago.`;
  if (hours < 24) return `Checked ${hours} hour${hours === 1 ? "" : "s"} ago.`;
  return `Checked ${formatSyncTimestamp(observed)}.`;
};

export const renderSyncChecked = (host: HTMLTimeElement | null, observed: string): void => {
  if (!host) return;
  host.hidden = !observed;
  host.textContent = syncCheckedText(observed);
  host.dateTime = Number.isFinite(Date.parse(observed)) ? observed : "";
  host.title = host.dateTime
    ? new Date(observed).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "long" })
    : observed;
  host.setAttribute("aria-label", observed ? `Checked ${host.title}` : "");
  const exact = host.closest("[data-sync-panel]")?.querySelector<HTMLTimeElement>("[data-sync-checked-exact]");
  if (exact) {
    exact.hidden = !observed;
    exact.dateTime = host.dateTime;
    exact.textContent = `Checked ${host.title}.`;
  }
};
