import { asRecord } from "./api-internal";
import { editorURLForKey, postManagement, type ManageOutcome } from "./source-manage-client";
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
  let pending: { signature: string; id: string } | undefined;
  let busy = false;
  let committed = false;
  const values = (): { title: string; folder: string } => ({
    title: title.value.trim(), folder: folder.value.trim().replace(/\\/g, "/"),
  });
  const preview = (): void => {
    const value = values();
    const name = value.title.replace(/\.md$/i, "");
    destination.textContent = name
      ? `Destination: ${value.folder ? value.folder + "/" : ""}${name}.md`
      : "Enter a title to see where your note will be saved.";
  };
  form.addEventListener("input", preview);
  preview();

  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (busy || committed || !form.reportValidity()) return;
    const value = values();
    const signature = JSON.stringify(value);
    if (pending?.signature !== signature) pending = { signature, id: newSaveID() };
    busy = true;
    button.disabled = title.disabled = folder.disabled = true;
    status.textContent = "Creating note…";
    try {
      const result = await postManagement({
        url: form.dataset.createEndpoint!, token: form.dataset.csrf!, fetch: deps.fetch,
        body: new URLSearchParams({ ...value, save_id: pending.id }),
      });
      committed = showOutcome(result, status, form.dataset.editorPath!, navigate);
    } catch {
      status.textContent = unconfirmedMessage;
    } finally {
      busy = false;
      button.disabled = title.disabled = folder.disabled = committed;
    }
  });
};

const unconfirmedMessage = "Creation not confirmed. Retry with the same title and folder to check safely.";

const showOutcome = (
  result: ManageOutcome, status: HTMLElement, editorPath: string, navigate: (url: string) => void,
): boolean => {
  if (result.ok && result.key) {
    navigate(editorURLForKey(editorPath, result.key));
    return true;
  }
  const payload = asRecord(result.payload);
  const committed = payload?.changed === true;
  status.textContent = result.unconfirmed || result.ok ? unconfirmedMessage : result.message;
  if (committed && typeof payload?.key === "string") {
    const link = document.createElement("a");
    link.href = editorURLForKey(editorPath, payload.key);
    link.textContent = "Open the created note";
    status.append(" ", link);
  }
  return committed;
};
