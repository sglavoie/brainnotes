import { readableExcerpt } from "./note-excerpt";
import { noteKeyFromPath, readNoteReference, type NoteReference } from "./note-reference";
import { highlightedText } from "./text-highlight";

// Preview only the two displayed sources. These reads never record an open or
// change ranking, and a late answer cannot populate a replacement card.
export const enhanceBriefConnection = (card: HTMLElement): void => {
  if (card.dataset.connectionPreview) return;
  const source = card.querySelector<HTMLAnchorElement>(".brief-connection-source a");
  const candidate = card.querySelector<HTMLAnchorElement>(".brief-card-title a");
  if (!source || !candidate) return;
  card.dataset.connectionPreview = "loading";
  const controller = new AbortController();
  const stop = (): void => controller.abort();
  window.addEventListener("pagehide", stop, { once: true });
  const keys = [noteKeyFromPath(source.href), noteKeyFromPath(candidate.href)];
  void Promise.all(keys.map((key, index) => readNoteReference(key, controller.signal, card.dataset.connectionBridge || keys[1 - index])))
    .then((notes) => {
      if (!card.isConnected || controller.signal.aborted) return;
      const pair = document.createElement("div");
      pair.className = "brief-connection-passages";
      pair.setAttribute("aria-label", "Passages to explore together");
      for (const note of notes) pair.append(passage(note));
      const excerpt = card.querySelector(".brief-card-excerpt");
      if (excerpt) excerpt.replaceWith(pair);
      else card.querySelector(".brief-connection-source")?.after(pair);
      card.dataset.connectionPreview = "ready";
    }).catch(() => { delete card.dataset.connectionPreview; })
    .finally(() => window.removeEventListener("pagehide", stop));
};

const passage = (note: NoteReference): HTMLElement => {
  const quote = document.createElement("blockquote");
  const label = document.createElement("cite");
  label.textContent = note.title;
  const text = document.createElement("p");
  const blocks = note.passages?.filter((block) => !block.heading) || [];
  const excerpt = readableExcerpt(blocks.find((block) => block.relevant)?.text || blocks[0]?.text || note.excerpt) || "This note has no prose yet.";
  text.append(highlightedText(excerpt, note.context ? [note.context] : []));
  quote.append(label, text);
  return quote;
};
