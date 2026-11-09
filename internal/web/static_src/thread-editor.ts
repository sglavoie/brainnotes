import type { SourceEditorController } from "./source-editor-controller";
import { prepareThread, threadGroupSelect, type ThreadItem } from "./thread-client";

const button = (text: string, action: () => void): HTMLButtonElement => {
  const element = document.createElement("button"); element.type = "button"; element.className = "bn-button"; element.textContent = text;
  element.addEventListener("click", action); return element;
};

export const initThreadEditor = (panel: HTMLElement, editor: SourceEditorController, token: string, key: string): void => {
  const host = panel.querySelector<HTMLElement>("[data-thread-material]");
  if (!host) return;
  const heading = document.createElement("h2"); heading.textContent = "Collected material";
  const hint = document.createElement("p"); hint.textContent = "Add passages while reading. Arrange them here, then Save. Your question and draft stay in the editor.";
  const status = document.createElement("p"); status.setAttribute("role", "status");
  const list = document.createElement("div");
  let busy = false;
  let lastContent = "";
  const refresh = async (): Promise<void> => {
    const before = editor.content(); if (before === null || busy) return;
    busy = true;
    try {
      const prepared = await prepareThread(token, before, "inspect");
      if (editor.content() !== before) { status.textContent = "Writing changed. Refresh material to see the latest collection."; return; }
      lastContent = before;
      list.replaceChildren(...prepared.items.map((item, index) => renderItem(item, index, prepared.items.length, change)));
      if (!prepared.items.length) list.textContent = "No passages yet. Open a note and choose Add to thread.";
      status.textContent = "";
    } catch (error) { status.textContent = error instanceof Error ? error.message : "Could not load collected material."; }
    finally { busy = false; }
  };
  const change = async (action: string, index: number, value = ""): Promise<void> => {
    const before = editor.content();
    if (busy || before === null) return;
    if (collectionBytes(before) !== collectionBytes(lastContent)) {
      status.textContent = "The collection changed in the editor. Your commentary is kept here; review it before refreshing material."; return;
    }
    busy = true;
    try {
      const prepared = await prepareThread(token, before, action, { index: String(index), group: value, commentary: value });
      if (!editor.replaceContent(before, prepared.content)) { status.textContent = "Writing changed while preparing this edit. Your text is kept; try again."; return; }
      lastContent = prepared.content;
      list.replaceChildren(...prepared.items.map((item, i) => renderItem(item, i, prepared.items.length, change)));
      status.textContent = "Arrangement updated in the editor. Choose Save to keep it in the vault.";
      list.querySelectorAll<HTMLElement>("article")[index]?.focus();
    } catch (error) { status.textContent = error instanceof Error ? error.message : "Could not arrange this passage."; }
    finally { busy = false; }
  };
  const synthesize = button("Open in synthesis", () => void openSynthesis(editor, key, status));
  host.replaceChildren(heading, hint, button("Refresh material", () => void refresh()), synthesize, status, list);
  void refresh();
};

const renderItem = (item: ThreadItem, index: number, count: number, change: (action: string, index: number, value?: string) => Promise<void>): HTMLElement => {
  const article = document.createElement("article"); article.className = "thread-passage"; article.tabIndex = -1;
  const title = document.createElement("h3");
  if (item.path) { const link = document.createElement("a"); link.href = item.path; link.textContent = item.title; title.append(link); }
  else title.textContent = item.title;
  const quote = document.createElement("blockquote"); quote.textContent = item.quote || "Whole-note source";
  const commentary = document.createElement("p"); commentary.textContent = item.commentary;
  const group = threadGroupSelect(); group.value = item.group; group.setAttribute("aria-label", `Group for passage ${index + 1}`);
  group.addEventListener("change", () => void change("group", index, group.value));
  const up = button("Move earlier", () => void change("up", index)); up.disabled = index === 0;
  const down = button("Move later", () => void change("down", index)); down.disabled = index === count - 1;
  article.append(title, quote, commentary, group, up, down, button("Remove passage", () => void change("remove", index)));
  const details = document.createElement("details"); const summary = document.createElement("summary"); summary.textContent = "Edit commentary";
  const label = document.createElement("label"); label.textContent = "Why this matters";
  const writing = document.createElement("textarea"); writing.value = item.commentary; writing.maxLength = 8000; label.append(writing);
  details.append(summary, label, button("Update commentary", () => void change("comment", index, writing.value))); article.append(details);
  if (item.warning) { const warning = document.createElement("p"); warning.className = "review-warning"; warning.textContent = item.warning; article.append(warning); }
  return article;
};

const openSynthesis = async (editor: SourceEditorController, key: string, status: HTMLElement): Promise<void> => {
  if (editor.dirty()) {
    status.textContent = "Save the thread before opening its collected sources in synthesis.";
    return;
  }
  window.location.assign(`/synthesize?source=${encodeURIComponent(key)}&thread=1`);
};

const collectionBytes = (content: string): string => {
  const start = content.indexOf("<!-- bn:thread:v1 -->");
  const end = content.indexOf("<!-- bn:thread:end -->");
  return start >= 0 && end > start ? content.slice(start, end) : content;
};
