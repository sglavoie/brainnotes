import { initNewNote } from "./new-note";

// Reuse the server-validated sources and ordinary creation receipt/recovery.
// Fetching the form is read-only; only submitting its title creates a note.
type ConnectionWritingRequest = { host: HTMLElement; sources: string[]; signal: AbortSignal };

const openWriting = ({ host, sources, signal }: ConnectionWritingRequest): void => {
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
    form.dataset.newNoteContext = params.toString();
    // This may be opened from New note; all label and status IDs stay unique.
    for (const element of [form, ...form.querySelectorAll<HTMLElement>("*")]) {
      for (const attribute of ["id", "for", "aria-describedby"]) {
        const value = element.getAttribute(attribute);
        if (value) element.setAttribute(attribute, value.split(" ").map((id) => `connection-${id}`).join(" "));
      }
      element.removeAttribute("autofocus");
    }
    const thought = form.querySelector<HTMLTextAreaElement>('[name="body"]')!;
    form.dataset.newNotePrefix = thought.value;
    thought.value = "";
    thought.maxLength = Math.max(0, 20000 - form.dataset.newNotePrefix.length);
    host.replaceChildren(form);
    initNewNote(host);
    arrangeWriting(form);
  }).catch(() => {
    if (signal.aborted) return;
    status.textContent = "Writing space unavailable. Neither source note has changed.";
    const retry = document.createElement("button");
    retry.type = "button"; retry.className = "bn-button"; retry.textContent = "Retry writing space";
    retry.addEventListener("click", () => openWriting({ host, sources, signal }));
    host.append(retry);
  });
};

document.addEventListener("bn:connection-writing", (event) => {
  openWriting((event as CustomEvent<ConnectionWritingRequest>).detail);
});

const arrangeWriting = (form: HTMLFormElement): void => {
  const body = form.querySelector<HTMLTextAreaElement>('[name="body"]')!;
  const bodyLabel = form.querySelector<HTMLLabelElement>(`label[for="${body.id}"]`)!;
  bodyLabel.textContent = "Your thought";
  const title = form.querySelector<HTMLInputElement>('[name="title"]')!;
  const submit = form.querySelector<HTMLButtonElement>('button[type="submit"]')!;
  const naming = document.createElement("div"); naming.className = "connection-naming";
  while (form.firstElementChild && form.firstElementChild !== bodyLabel) naming.append(form.firstElementChild);
  form.insertBefore(naming, submit.parentElement);
  naming.hidden = !title.value;
  submit.hidden = naming.hidden;
  const save = document.createElement("button");
  save.type = "button"; save.className = "bn-button bn-primary"; save.textContent = "Save thought…";
  save.hidden = !naming.hidden;
  save.addEventListener("click", () => {
    naming.hidden = false; submit.hidden = false; save.hidden = true; title.focus();
  });
  body.after(save);
  body.focus({ preventScroll: true });
  body.setSelectionRange(body.value.length, body.value.length);
};
