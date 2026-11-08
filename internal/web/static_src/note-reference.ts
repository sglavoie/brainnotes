import { readableExcerpt } from "./note-excerpt";
import { withLibraryContext } from "./note-navigation";

export type NoteReference = {
  title: string; excerpt: string; path: string;
  passages?: { text: string; heading: boolean; relevant: boolean }[];
  truncated?: boolean;
};

export const noteKeyFromPath = (path: string): string => {
  const url = new URL(path, window.location.origin);
  return decodeURIComponent(url.pathname.replace(/^\/n\//, "")).replace(/\.md$/i, "");
};

export const readNoteReference = async (key: string, signal: AbortSignal, context = ""): Promise<NoteReference> => {
  const response = await fetch(`/api/note/peek?${new URLSearchParams({ key, context })}`, { signal });
  if (!response.ok) throw new Error("This note is unavailable. Try again or open it directly.");
  return await response.json() as NoteReference;
};

export const referenceLink = (reference: NoteReference): HTMLAnchorElement => {
  const link = document.createElement("a");
  link.href = withLibraryContext(reference.path);
  link.textContent = reference.title;
  return link;
};

export const renderReference = (root: HTMLElement, reference: NoteReference, connection = false): void => {
  root.replaceChildren();
  const title = document.createElement("h3");
  title.append(referenceLink(reference));
  root.append(title);
  const shown = referencePassages(reference, connection);
  for (const passage of shown) {
    const text = document.createElement(passage.heading ? "h4" : "p");
    text.textContent = readableExcerpt(passage.text);
    if (passage.relevant) text.className = "connection-passage";
    root.append(text);
  }
  if (!shown.length) root.append("This note has no prose yet.");
  if (reference.truncated && !connection) {
    const notice = document.createElement("p");
    notice.className = "reference-limit";
    notice.textContent = "Showing the opening passages. Open the note to read everything.";
    root.append(notice);
  }
};

const referencePassages = (reference: NoteReference, connection: boolean) => {
  const passages = (reference.passages || [{ text: reference.excerpt, heading: false, relevant: false }])
    .filter((passage, index) => !(index === 0 && passage.heading && passage.text.trim() === reference.title.trim()));
  if (!connection) return passages;
  const relevant = passages.filter((passage) => passage.relevant);
  return relevant.length ? relevant : passages.filter((passage) => !passage.heading).slice(0, 2);
};
