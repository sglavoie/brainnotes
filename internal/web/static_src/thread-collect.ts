import { threadCatalog, threadClient, prepareThread, threadGroupSelect, type ThreadCatalog } from "./thread-client";
import type { CollectRequest } from "./thread-loader";
import { collectionStorage, type ThreadAttempt } from "./thread-collect-state";
import { newSaveID } from "./source-save";

const labelled = (text: string, control: HTMLElement): HTMLDivElement => {
  const wrapper = document.createElement("div");
  const label = document.createElement("label"); label.textContent = text;
  control.id = `thread-field-${crypto.randomUUID()}`; label.htmlFor = control.id;
  wrapper.append(label, control); return wrapper;
};

const openCollection = (request: CollectRequest): void => {
  if (!document.querySelector('link[href="/static/threads.css"]')) {
    const style = document.createElement("link"); style.rel = "stylesheet"; style.href = "/static/threads.css"; document.head.append(style);
  }
  const dialog = document.createElement("dialog"); dialog.className = "thread-collection-dialog";
  dialog.setAttribute("aria-label", "Add to thread");
  const title = document.createElement("h2"); title.textContent = "Add to thread";
  const close = document.createElement("button"); close.type = "button"; close.className = "bn-button"; close.textContent = "Return to reading";
  close.addEventListener("click", () => dialog.close());
  const quote = document.createElement("blockquote"); quote.textContent = request.quote || "Collect this note as a source.";
  const host = document.createElement("div");
  const status = document.createElement("p"); status.setAttribute("role", "status"); status.textContent = "Loading threads…";
  dialog.append(title, close, quote, host, status);
  dialog.addEventListener("close", () => { dialog.remove(); request.returnTo.focus({ preventScroll: true }); });
  document.body.append(dialog); dialog.showModal();
  const load = async (): Promise<void> => {
    try { const catalog = await threadCatalog(); if (dialog.isConnected) collectionForm(request, catalog, host, status); }
    catch (error) {
      status.textContent = error instanceof Error ? error.message : "Threads could not load.";
      const retry = document.createElement("button"); retry.textContent = "Retry threads";
      retry.addEventListener("click", () => { host.replaceChildren(); void load(); }); host.replaceChildren(retry);
    }
  };
  void load();
};

const collectionForm = (request: CollectRequest, catalog: ThreadCatalog, host: HTMLElement, status: HTMLElement): void => {
  const start = document.createElement("a"); start.href = "/threads"; start.target = "_blank"; start.rel = "noopener"; start.textContent = "Start a thread in a new tab";
  if (!catalog.items.length) { status.textContent = "Create a thread, then reopen Add to thread to collect this passage."; host.replaceChildren(start); return; }
  const form = document.createElement("form");
  const target = document.createElement("select"); target.required = true;
  for (const item of catalog.items) target.add(new Option(item.title, item.key));
  const group = threadGroupSelect();
  const commentary = document.createElement("textarea"); commentary.rows = 3; commentary.maxLength = 8000;
  const submit = document.createElement("button"); submit.type = "submit"; submit.className = "bn-button bn-primary"; submit.textContent = "Add to thread";
  form.append(labelled("Thread", target), labelled("Group", group), labelled("Why this matters (optional)", commentary), submit);
  const recovery = document.createElement("p"); recovery.setAttribute("role", "status");
  host.replaceChildren(form, start, recovery); status.textContent = "Your quotation and source will be kept in the thread.";
  const storage = collectionStorage(catalog.vault_id, request, recovery);
  const restored = storage.read();
  if (restored) { target.value = restored.target; group.value = restored.group; commentary.value = restored.commentary; }
  let pending: ThreadAttempt | undefined = restored?.pending;
  let busy = false;
  const persist = (): void => storage.write({ target: target.value, group: group.value, commentary: commentary.value, pending });
  const controls = (): void => {
    target.disabled = group.disabled = commentary.disabled = busy || !!pending;
    submit.disabled = busy;
    submit.textContent = pending ? "Check addition" : "Add to thread";
  };
  controls();
  if (pending) status.textContent = "Addition not confirmed. Check addition retries the same saved request safely.";
  form.addEventListener("input", persist);
  form.addEventListener("submit", async (event) => {
    event.preventDefault(); if (busy || !form.reportValidity()) return;
    busy = true; controls();
    try {
      if (!pending) {
        const client = threadClient(target.value, catalog.token);
        const source = await client.read();
        const prepared = await prepareThread(catalog.token, source.content, "add", { source: request.source, source_revision: request.revision, quote: request.quote, group: group.value, commentary: commentary.value });
        pending = { key: target.value, content: prepared.content, revision: source.revision, saveID: newSaveID() };
        persist();
      }
      status.textContent = "Saving collected material…";
      const result = await threadClient(pending.key, catalog.token).save(pending);
      if (result.ok || result.changed) {
        const key = pending.key;
        storage.clear(); pending = undefined;
        status.textContent = result.ok ? "Added to thread." : result.message;
        const link = document.createElement("a"); link.href = `/edit?key=${encodeURIComponent(key)}`; link.textContent = "Open thread";
        form.replaceChildren(link); return;
      }
      status.textContent = result.message;
      if (result.code) { pending = undefined; persist(); }
    } catch (error) {
      status.textContent = pending ? "Addition not confirmed. Choose Check addition to retry the same request safely." : collectionError(error);
    } finally { busy = false; controls(); }
  });
};

document.addEventListener("bn:thread-collect", (event) => {
  const request = (event as CustomEvent<Partial<CollectRequest> & { returnTo: HTMLElement }>).detail;
  openCollection({
    source: request.source || request.returnTo.dataset.threadCollect || "",
    quote: request.quote || "",
    revision: request.revision || document.querySelector<HTMLElement>("[data-note-body]")?.dataset.noteRevision || "",
    returnTo: request.returnTo,
  });
});

const collectionError = (error: unknown): string => error instanceof Error ? error.message : "Could not prepare this addition.";
