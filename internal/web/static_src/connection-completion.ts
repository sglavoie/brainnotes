import { notePathForKey } from "./filters";
import { withLibraryContext } from "./note-navigation";
import { noteKeyFromPath } from "./note-reference";

export const suggestThoughtTitle = (thought: string): string => {
  const line = thought.trim().split(/\n|[.!?](?:\s|$)/u)[0] || "";
  return line.replace(/\[\[([^\]|]+)\|?([^\]]*)\]\]/g, (_match, target: string, alias: string) => alias || target)
    .replace(/[#*_`~]/g, "").replace(/[\\/:<>"|?\u0000-\u001f]/g, " ")
    .replace(/\s+/g, " ").trim().slice(0, 80).trim().replace(/[. ]+$/, "");
};

// Called only by the normal creation controller's confirmed-success callback.
export const showSavedThought = (form: HTMLFormElement, editorURL: string): void => {
  const key = new URL(editorURL, location.origin).searchParams.get("key");
  if (!key) return;
  const title = form.querySelector<HTMLInputElement>('[name="title"]')!.value;
  const thought = form.querySelector<HTMLTextAreaElement>('[name="body"]')!.value;
  const result = document.createElement("section"); result.className = "connection-saved";
  const single = form.dataset.thoughtSourceCount === "1";
  announceSavedPassage(form, key, title, thought);
  const status = document.createElement("p"); status.setAttribute("role", "status");
  status.textContent = single ? "Thought saved with your quotation and source." : "Thought saved, linked to both sources.";
  const heading = document.createElement("h3"); heading.textContent = title; heading.tabIndex = -1;
  const body = document.createElement("p"); body.className = "connection-saved-thought"; body.textContent = thought;
  const actions = document.createElement("div"); actions.className = "connection-saved-actions";
  const keep = document.createElement("button"); keep.type = "button"; keep.className = "bn-button bn-primary"; keep.textContent = single ? "Back to your passage" : "Keep exploring";
  keep.addEventListener("click", () => form.closest<HTMLDialogElement>("dialog")?.close());
  const open = document.createElement("a"); open.className = "bn-button"; open.textContent = "Open note";
  open.href = withLibraryContext(notePathForKey(key));
  const sources = document.createElement("div"); sources.className = "thought-source-links";
  const previews = [...(form.closest("dialog")?.querySelectorAll<HTMLAnchorElement>(".connection-note h3 a, .thought-source-link") || [])];
  for (const key of new URLSearchParams(form.dataset.newNoteContext).getAll("source")) {
    const preview = previews.find((link) => noteKeyFromPath(link.href).toLowerCase() === key.toLowerCase());
    const source = document.createElement("a"); source.href = withLibraryContext(notePathForKey(key));
    source.textContent = preview?.textContent || key;
    sources.append(source);
  }
  sources.setAttribute("aria-label", "Source notes");
  actions.append(keep, open); result.append(status, sources, heading, body, actions); form.replaceChildren(result);
  heading.focus({ preventScroll: true });
};

const announceSavedPassage = (form: HTMLFormElement, key: string, title: string, thought: string): void => {
  const context = new URLSearchParams(form.dataset.newNoteContext);
  if (form.dataset.thoughtSourceCount !== "1" || !context.get("passage")) return;
  document.dispatchEvent(new CustomEvent("bn:thought-saved", {
    detail: { source: context.get("source"), quote: context.get("passage"), path: notePathForKey(key), title, thought },
  }));
};
