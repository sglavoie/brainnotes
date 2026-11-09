import { showSavedThought, suggestThoughtTitle } from "./connection-completion";
import { initNewNote } from "./new-note";
import type { ThoughtWritingRequest } from "./connection-writing-loader";
import { quotedPassage } from "./thought-quote";

// Reuse the server-validated sources and ordinary creation receipt/recovery.
// Fetching the form is read-only; only submitting its title creates a note.
const openWriting = ({ host, sources, signal, passage }: ThoughtWritingRequest): void => {
  const status = document.createElement("p"); status.setAttribute("role", "status");
  status.textContent = "Preparing your writing space…";
  host.replaceChildren(status);
  const params = new URLSearchParams();
  for (const source of sources) params.append("source", source);
  void fetch(`/new?${params}`, { signal }).then(async (response) => {
    if (!response.ok) throw new Error("Creation unavailable");
    const parsed = new DOMParser().parseFromString(await response.text(), "text/html");
    const sourceForm = parsed.querySelector<HTMLFormElement>("[data-new-note]");
    if (signal.aborted) return;
    if (!sourceForm) throw new Error("Creation form unavailable");
    const form = document.importNode(sourceForm, true);
    const context = new URLSearchParams(params);
    if (passage) context.set("passage", passage);
    form.dataset.newNoteContext = context.toString();
    form.dataset.thoughtSourceCount = String(sources.length);
    // This may be opened from New note; all label and status IDs stay unique.
    for (const element of [form, ...form.querySelectorAll<HTMLElement>("*")]) {
      for (const attribute of ["id", "for", "aria-describedby"]) {
        const value = element.getAttribute(attribute);
        if (value) element.setAttribute(attribute, value.split(" ").map((id) => `connection-${id}`).join(" "));
      }
      element.removeAttribute("autofocus");
    }
    const thought = form.querySelector<HTMLTextAreaElement>('[name="body"]')!;
    form.dataset.newNotePrefix = thought.value + (passage ? quotedPassage(passage) : "");
    thought.value = "";
    thought.maxLength = Math.max(0, 20000 - form.dataset.newNotePrefix.length);
    host.replaceChildren(form);
    form.dataset.newNoteSubmitLabel = "Save thought";
    delete form.dataset.newNoteWriteFirst;
    initNewNote(host, { navigate: (url) => showSavedThought(form, url) });
    arrangeWriting(form);
  }).catch(() => {
    if (signal.aborted) return;
    status.textContent = "Writing space unavailable. Your source notes have not changed.";
    const retry = document.createElement("button");
    retry.type = "button"; retry.className = "bn-button"; retry.textContent = "Retry writing space";
    retry.addEventListener("click", () => openWriting({ host, sources, signal, passage }));
    host.append(retry);
  });
};

document.addEventListener("bn:connection-writing", (event) => {
  openWriting((event as CustomEvent<ThoughtWritingRequest>).detail);
});

const arrangeWriting = (form: HTMLFormElement): void => {
  const body = form.querySelector<HTMLTextAreaElement>('[name="body"]')!;
  const bodyLabel = form.querySelector<HTMLLabelElement>(`label[for="${body.id}"]`)!;
  bodyLabel.textContent = "Your thought";
  const title = form.querySelector<HTMLInputElement>('[name="title"]')!;
  const submit = form.querySelector<HTMLButtonElement>('button[type="submit"]')!;
  const naming = form.querySelector<HTMLElement>(".new-note-naming")!;
  naming.classList.add("connection-naming");
  naming.hidden = !title.value;
  submit.hidden = naming.hidden;
  const save = document.createElement("button");
  save.type = "button"; save.className = "bn-button bn-primary"; save.textContent = "Save thought…";
  save.hidden = !naming.hidden;
  save.addEventListener("click", () => {
    if (!title.value.trim()) title.value = suggestThoughtTitle(body.value);
    title.dispatchEvent(new Event("input", { bubbles: true }));
    naming.hidden = false; submit.hidden = false; save.hidden = true; title.focus();
  });
  const hint = document.createElement("p");
  hint.className = "connection-save-hint";
  hint.textContent = form.dataset.thoughtSourceCount === "1"
    ? "Your quotation and its source are included in the new note." : "Creates a note linked to both sources.";
  body.after(save, hint);
  body.focus({ preventScroll: true });
  body.setSelectionRange(body.value.length, body.value.length);
};
