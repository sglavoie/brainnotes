import { noteKeyFromPath } from "./note-reference";
import { loadWorkspaceScript } from "./workspace-script";
import { loadThoughtStyles } from "./thought-styles";

export type Connection = { source: string; candidate: string; bridge?: string; relationship?: string };

export const connectionButton = (connection: Connection): HTMLButtonElement => {
  const button = document.createElement("button");
  button.type = "button";
  button.className = "bn-button connection-launch";
  button.textContent = "Explore connection";
  button.addEventListener("click", () => openConnection(connection, button));
  return button;
};

export const openBriefConnection = (button: HTMLButtonElement): boolean => {
  if (button.dataset.briefSlot !== "connect") return false;
  const card = button.closest<HTMLElement>("[data-brief-card]");
  const source = card?.querySelector<HTMLAnchorElement>(".brief-connection-source a");
  const candidate = card?.querySelector<HTMLAnchorElement>(".brief-card-title a");
  if (!source || !candidate) return false;
  openConnection({ source: noteKeyFromPath(source.href), candidate: noteKeyFromPath(candidate.href), bridge: card?.dataset.connectionBridge }, button);
  return true;
};

export const openConnection = (connection: Connection, trigger: HTMLElement): void => {
  if (trigger.getAttribute("aria-busy") === "true") return;
  trigger.setAttribute("aria-busy", "true");
  void Promise.all([loadThoughtStyles(), loadWorkspaceScript("connection-dialog")]).then(() => {
    if (trigger.isConnected) document.dispatchEvent(new CustomEvent("bn:open-connection", { detail: { connection, trigger } }));
  }).catch(() => {
    const status = document.createElement("p"); status.setAttribute("role", "status");
    status.textContent = "Connection tools unavailable. Try again."; trigger.after(status);
  }).finally(() => trigger.removeAttribute("aria-busy"));
};
