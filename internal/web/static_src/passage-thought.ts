import { startConnectionWriting } from "./thought-writing-loader";
import { notePathForKey } from "./filters";

// Keep the selected words in memory while focus moves to the writing dialog.
// Only a confirmed ordinary note creation writes anything to the vault.
export const initPassageThought = (body: HTMLElement): void => {
  const key = body.dataset.noteKey;
  if (!key || document.documentElement.dataset.readOnly === "true") return;
  const toolbar = document.createElement("div");
  toolbar.className = "passage-tools";
  toolbar.hidden = true;
  const button = document.createElement("button");
  button.type = "button"; button.className = "bn-button bn-primary";
  button.textContent = "Write a thought";
  const hint = document.createElement("span"); hint.textContent = "Quote and source included";
  toolbar.append(button, hint); document.body.append(toolbar);
  let passage = "";
  let range: Range | undefined;
  const update = (): void => {
    const selection = window.getSelection();
    if (!selection?.rangeCount || selection.isCollapsed) {
      if (!toolbar.contains(document.activeElement)) toolbar.hidden = true;
      return;
    }
    const selected = selection.getRangeAt(0);
    const text = selection.toString().trim();
    if (!body.contains(selected.startContainer) || !body.contains(selected.endContainer) || !text || text.length > 8000) {
      toolbar.hidden = true; return;
    }
    passage = text; range = selected.cloneRange(); toolbar.hidden = false;
  };
  document.addEventListener("selectionchange", update);
  update();
  // Keyboard users can reach the same action without tabbing through the rest
  // of a long note; touch users retain the native selection handles and menu.
  body.addEventListener("keydown", (event) => {
    if (event.altKey && event.key === "Enter" && !toolbar.hidden) { event.preventDefault(); button.click(); }
  });
  button.addEventListener("click", () => {
    if (!passage || !range) return;
    toolbar.hidden = true;
    openPassageThought(body, key, passage, range.cloneRange());
  });
};

const noteBody = document.querySelector<HTMLElement>("[data-note-body]");
if (noteBody) initPassageThought(noteBody);

const openPassageThought = (body: HTMLElement, key: string, passage: string, range: Range): void => {
  const dialog = document.createElement("dialog");
  dialog.className = "connection-explorer passage-thought";
  dialog.setAttribute("aria-labelledby", "passage-thought-title");
  const header = document.createElement("header");
  const heading = document.createElement("h2"); heading.id = "passage-thought-title"; heading.textContent = "Follow this thought";
  const close = document.createElement("button"); close.type = "button"; close.className = "bn-button"; close.textContent = "Return to reading";
  close.addEventListener("click", () => dialog.close()); header.append(heading, close);
  const quote = document.createElement("blockquote"); quote.className = "thought-source-quote"; quote.textContent = passage;
  const source = document.createElement("p"); source.className = "connection-explanation";
  const link = document.createElement("a"); link.className = "thought-source-link"; link.href = notePathForKey(key);
  link.textContent = document.querySelector(".reading-title")?.textContent || key;
  source.append("From ", link);
  const host = document.createElement("section"); host.className = "connection-writing";
  dialog.append(header, source, quote, host);
  const controller = new AbortController();
  const scroll = { x: window.scrollX, y: window.scrollY };
  dialog.addEventListener("close", () => {
    controller.abort(); dialog.remove(); body.focus({ preventScroll: true });
    // addRange attaches its argument by reference. Keep our saved range out of
    // the live selection, where the next click could collapse it in place.
    window.getSelection()?.removeAllRanges(); window.getSelection()?.addRange(range.cloneRange());
    window.scrollTo(scroll.x, scroll.y);
  });
  document.body.append(dialog); dialog.showModal();
  startConnectionWriting({ host, sources: [key], signal: controller.signal, passage });
};
