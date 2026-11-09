import { notePathForKey } from "./filters";
import { withLibraryContext } from "./note-navigation";

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
  const status = document.createElement("p"); status.setAttribute("role", "status"); status.textContent = "Thought saved, linked to both sources.";
  const heading = document.createElement("h3"); heading.textContent = title; heading.tabIndex = -1;
  const body = document.createElement("p"); body.className = "connection-saved-thought"; body.textContent = thought;
  const actions = document.createElement("div"); actions.className = "connection-saved-actions";
  const keep = document.createElement("button"); keep.type = "button"; keep.className = "bn-button bn-primary"; keep.textContent = "Keep exploring";
  keep.addEventListener("click", () => form.closest<HTMLDialogElement>("dialog")?.close());
  const open = document.createElement("a"); open.className = "bn-button"; open.textContent = "Open note";
  open.href = withLibraryContext(notePathForKey(key));
  actions.append(keep, open); result.append(status, heading, body, actions); form.replaceChildren(result);
  heading.focus({ preventScroll: true });
};
