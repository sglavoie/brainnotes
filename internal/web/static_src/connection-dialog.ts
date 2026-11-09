import { connectionWriting } from "./connection-writing-loader";
import { readNoteReference, renderReference, referenceLink } from "./note-reference";
import { notePathForKey } from "./filters";
import type { Connection } from "./connection-explorer";

export const openConnection = (connection: Connection, trigger: HTMLElement): void => {
  const dialog = document.createElement("dialog");
  dialog.className = "connection-explorer";
  dialog.setAttribute("aria-labelledby", "connection-title");
  const header = document.createElement("header");
  const title = document.createElement("h2");
  title.id = "connection-title";
  title.textContent = "Where these ideas meet";
  const close = document.createElement("button");
  close.type = "button";
  close.className = "bn-button";
  close.textContent = "Close connection";
  close.addEventListener("click", () => dialog.close());
  header.append(title, close);
  const explanation = document.createElement("p");
  explanation.className = "connection-explanation";
  explanation.textContent = connection.relationship || "Read these passages together. What do you notice?";
  const pair = document.createElement("div");
  pair.className = "connection-pair";
  const request = new AbortController();
  pair.append(connectionNote(connection.source, connection.bridge || connection.candidate, request.signal), connectionNote(connection.candidate, connection.bridge || connection.source, request.signal));
  dialog.append(header, explanation, pair);
  if (connection.bridge) {
    explanation.classList.add("connection-bridge");
    void showBridge(explanation, connection.bridge, request.signal);
  }
  if (document.documentElement.dataset.readOnly !== "true") {
    connectionWriting(dialog, [connection.source, connection.candidate], request.signal);
  }
  dialog.addEventListener("close", () => {
    request.abort(); dialog.remove();
    if (trigger.isConnected) trigger.focus({ preventScroll: true });
  });
  document.body.append(dialog);
  dialog.showModal();
};

const connectionNote = (key: string, context: string, signal: AbortSignal): HTMLElement => {
  const article = document.createElement("article");
  article.className = "connection-note";
  article.setAttribute("aria-live", "polite");
  const load = async (): Promise<void> => {
    article.textContent = "Loading passage…";
    try {
      const note = await readNoteReference(key, signal, context);
      if (!signal.aborted) renderReference(article, note, true);
    } catch {
      if (signal.aborted) return;
      article.textContent = "This passage is unavailable. ";
      const retry = document.createElement("button");
      retry.type = "button"; retry.className = "bn-button"; retry.textContent = "Retry passage";
      retry.addEventListener("click", () => { void load(); });
      const open = document.createElement("a");
      open.href = notePathForKey(key); open.textContent = "Open note";
      article.append(retry, " ", open);
    }
  };
  void load();
  return article;
};

const showBridge = async (root: HTMLElement, key: string, signal: AbortSignal): Promise<void> => {
  try {
    const note = await readNoteReference(key.replace(/\.md$/i, ""), signal);
    if (!signal.aborted) root.replaceChildren("Connected through ", referenceLink(note));
  } catch { /* The pair remains useful without a third preview. */ }
};

document.addEventListener("bn:open-connection", (event) => {
  const { connection, trigger } = (event as CustomEvent<{ connection: Connection; trigger: HTMLElement }>).detail;
  openConnection(connection, trigger);
});
