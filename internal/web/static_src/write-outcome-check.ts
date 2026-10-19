import { announceConfirmedWrite } from "./write-notice";

type FetchLike = (input: string, init?: RequestInit) => Promise<Response>;
type PendingOutcome = { id: string; label: string };
const storageKey = "bn.pending-write-outcomes";

const pendingOutcomes = (): PendingOutcome[] => {
  try {
    const value: unknown = JSON.parse(sessionStorage.getItem(storageKey) || "[]");
    if (!Array.isArray(value)) return [];
    return value.filter((item): item is PendingOutcome =>
      !!item && typeof item === "object" &&
      typeof item.id === "string" && /^[0-9a-f]{32}$/.test(item.id) &&
      typeof item.label === "string" && item.label.length <= 60
    ).slice(-5);
  } catch {
    return [];
  }
};

const savePending = (items: PendingOutcome[]): void => {
  try { sessionStorage.setItem(storageKey, JSON.stringify(items.slice(-5))); } catch { /* Browser storage unavailable. */ }
};

const rememberPending = (id: string, label: string): void => {
  savePending([...pendingOutcomes().filter((item) => item.id !== id), { id, label }]);
};

const forgetPending = (id: string): void => {
  savePending(pendingOutcomes().filter((item) => item.id !== id));
};

// An attempt ID is only a correlation key. CSRF and the route's existing
// preview or revision checks still authorize and protect the write.
export const newWriteOperationID = (): string => {
  try {
    const bytes = globalThis.crypto.getRandomValues(new Uint8Array(16));
    return Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("");
  } catch {
    return "";
  }
};

export const offerWriteOutcomeCheck = (
  status: HTMLElement,
  operationID: string,
  fetcher: FetchLike,
  label = "Change",
  onConfirmed?: () => void
): void => {
  if (!operationID) return;
  status.classList.add("has-write-outcome-check");
  rememberPending(operationID, label);
  const button = status.ownerDocument.createElement("button");
  button.type = "button";
  button.className = "bn-button write-outcome-check";
  button.textContent = "Check outcome";
  const check = async (): Promise<void> => {
    button.disabled = true;
    try {
      const response = await fetcher(`/api/operations/outcome?id=${operationID}`, {
        headers: { Accept: "application/json" },
      });
      const result: unknown = await response.json();
      if (!response.ok || !result || typeof result !== "object") throw new Error("unavailable");
      const record = result as Record<string, unknown>;
      if (record.ok !== true) throw new Error("unavailable");
      if (record.status === "confirmed" && typeof record.journal_id === "string" && record.journal_id) {
        forgetPending(operationID);
        announceConfirmedWrite();
        status.textContent = `${label} confirmed in History (entry ${record.journal_id}).`;
        const history = status.ownerDocument.createElement("a");
        history.href = "/history";
        history.textContent = "View History";
        status.append(" ", history);
        status.classList.remove("is-error");
        status.classList.add("is-success");
        onConfirmed?.();
        return;
      }
      status.textContent = `${label} is still unconfirmed. Do not resend this request; check again, then refresh current values before making a new preview if needed.`;
    } catch {
      status.textContent = "Could not check the outcome. Check again when the connection is available.";
    }
    button.disabled = false;
    status.append(" ", button);
  };
  button.addEventListener("click", () => { void check(); });
  status.append(" ", button);
};

// A lost reply remains checkable after reload or navigation in the same tab.
// This banner holds only random IDs and action labels, never note content.
export const initPendingWriteOutcomes = (
  doc: Document = document,
  fetcher: FetchLike = (input, init) => fetch(input, init)
): void => {
  const pending = pendingOutcomes();
  const main = doc.querySelector("main");
  if (!main || pending.length === 0) return;
  const section = doc.createElement("section");
  section.className = "panel pending-write-outcomes";
  section.setAttribute("aria-label", "Writes awaiting confirmation");
  const heading = doc.createElement("h2");
  heading.textContent = "Check earlier changes";
  section.append(heading);
  for (const item of pending) {
    const row = doc.createElement("div");
    const status = doc.createElement("p");
    status.setAttribute("role", "status");
    status.textContent = `${item.label} was not confirmed before this page loaded.`;
    offerWriteOutcomeCheck(status, item.id, fetcher, item.label, () => {
      status.classList.add("write-outcome-confirmation");
      section.insertAdjacentElement("afterend", status);
      row.remove();
      if (!section.querySelector(".pending-write-outcome")) section.remove();
    });
    const dismiss = doc.createElement("button");
    dismiss.type = "button";
    dismiss.className = "bn-button";
    dismiss.textContent = "Dismiss reminder";
    dismiss.addEventListener("click", () => {
      forgetPending(item.id);
      row.remove();
      if (!section.querySelector(".pending-write-outcome")) section.remove();
    });
    row.className = "pending-write-outcome";
    row.append(status, dismiss);
    section.append(row);
  }
  const header = main.querySelector(":scope > header");
  if (header) header.insertAdjacentElement("afterend", section);
  else main.prepend(section);
};
