import { asRecord } from "./api-internal";
import { editorURLForKey, postManagement, type ManageOutcome } from "./source-manage-client";
import { newNoteDraft, type PendingCreation } from "./new-note-draft";
import { withLibraryContext } from "./note-navigation";
import { newSaveID } from "./source-save";

type NewNoteDeps = { fetch?: typeof fetch; navigate?: (url: string) => void };

export const initNewNote = (root: ParentNode = document, deps: NewNoteDeps = {}): void => {
  const form = root.querySelector<HTMLFormElement>("[data-new-note]");
  if (!form) return;
  const title = form.querySelector<HTMLInputElement>('[name="title"]')!;
  const folder = form.querySelector<HTMLInputElement>('[name="folder"]')!;
  const button = form.querySelector<HTMLButtonElement>('button[type="submit"]')!;
  const status = form.querySelector<HTMLElement>("[data-new-note-status]")!;
  const destination = form.querySelector<HTMLElement>("[data-new-note-destination]")!;
  const navigate = deps.navigate ?? ((url: string) => window.location.assign(url));
  const draft = newNoteDraft(form);
  const restored = draft.restore();
  if (restored) { title.value = restored.title; folder.value = restored.folder; }
  let pending: PendingCreation | undefined = restored?.pending;
  let busy = false;
  let committed = false;
  const values = (): { title: string; folder: string } => ({
    title: title.value.trim(), folder: folder.value.trim().replace(/\\/g, "/"),
  });
  const keepDraft = (): void => draft.save({ title: title.value, folder: folder.value, pending });
  const preview = (): void => {
    const value = values();
    button.textContent = pending?.signature === JSON.stringify(value) ? "Check creation" : "Create and write";
    const name = value.title.replace(/\.md$/i, "");
    destination.textContent = name
      ? `Destination: ${value.folder ? value.folder + "/" : ""}${name}.md`
      : "Enter a title to see where your note will be saved.";
  };
  form.addEventListener("input", () => { preview(); keepDraft(); });
  preview();
  if (pending) status.textContent = unconfirmedMessage;

  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (busy || committed || !form.reportValidity()) return;
    const value = values();
    const signature = JSON.stringify(value);
    if (pending?.signature !== signature) pending = { signature, id: newSaveID() };
    keepDraft();
    busy = true;
    button.disabled = title.disabled = folder.disabled = true;
    status.textContent = "Creating note…";
    try {
      const result = await postManagement({
        url: form.dataset.createEndpoint!, token: form.dataset.csrf!, fetch: deps.fetch,
        body: new URLSearchParams({ ...value, save_id: pending.id }),
      });
      committed = showOutcome(result, status, form.dataset.editorPath!, (url) => {
        draft.clear();
        navigate(url);
      });
      if (committed) draft.clear();
      else if (!result.unconfirmed && !result.ok) { pending = undefined; keepDraft(); }
    } catch {
      status.textContent = unconfirmedMessage;
    } finally {
      busy = false;
      preview();
      button.disabled = title.disabled = folder.disabled = committed;
    }
  });
};

const unconfirmedMessage = "Creation not confirmed. Choose Check creation to check safely using the same title and folder.";

const showOutcome = (
  result: ManageOutcome, status: HTMLElement, editorPath: string, navigate: (url: string) => void,
): boolean => {
  if (result.ok && result.key) {
    navigate(withLibraryContext(editorURLForKey(editorPath, result.key), true));
    return true;
  }
  const payload = asRecord(result.payload);
  const committed = payload?.changed === true;
  status.textContent = result.unconfirmed || result.ok ? unconfirmedMessage : result.message;
  if (committed && typeof payload?.key === "string") {
    const link = document.createElement("a");
    link.href = withLibraryContext(editorURLForKey(editorPath, payload.key), true);
    link.textContent = "Open the created note";
    status.append(" ", link);
  }
  return committed;
};
