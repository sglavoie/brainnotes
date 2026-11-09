import type { CaptureResult } from "./api";

export const initGlobalCapture = (): void => {
  const dialog = document.querySelector<HTMLDialogElement>("[data-capture-dialog]");
  const host = dialog?.querySelector<HTMLElement>("[data-capture-dialog-content]");
  if (!dialog || !host) return;
  // On the Brief, move the live form. There is only one draft and one request
  // controller, even when a capture is still awaiting confirmation.
  const inline = document.querySelector<HTMLElement>(".capture-panel[data-capture-root]");
  const marker = document.createComment("inline capture position");
  if (inline) { inline.before(marker); host.replaceChildren(); }
  let origin: HTMLElement | null = null;
  const open = (): void => {
    if (dialog.open || document.querySelector("dialog[open]")) return;
    origin = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    if (inline) host.append(inline);
    dialog.showModal();
    dialog.querySelector<HTMLTextAreaElement>("[data-capture-input]:not(:disabled)")?.focus({ preventScroll: true });
  };
  dialog.addEventListener("close", () => {
    if (inline) marker.after(inline);
    origin?.focus({ preventScroll: true });
  });
  dialog.querySelector("[data-capture-close]")?.addEventListener("click", () => dialog.close());
  for (const button of document.querySelectorAll("[data-capture-launch]")) button.addEventListener("click", open);
  document.addEventListener("keydown", (event) => {
    if (event.code !== "Space" || !event.shiftKey || !(event.metaKey || event.ctrlKey) ||
        event.altKey || event.repeat || event.isComposing || event.defaultPrevented) return;
    event.preventDefault();
    open();
  });
  document.addEventListener("bn:capture-complete", (event) => {
    if (!dialog.open || !(event.target instanceof Node) || !dialog.contains(event.target)) return;
    const result = (event as CustomEvent<CaptureResult>).detail;
    dialog.close();
    showCaptureNotice(result, origin);
  });
};

const showCaptureNotice = (result: CaptureResult, origin: HTMLElement | null): void => {
  const notice = document.querySelector<HTMLElement>("[data-capture-notice]");
  if (!notice) return;
  notice.replaceChildren(document.createTextNode(result.message || "Captured to your inbox."));
  if (result.path?.startsWith("/n/")) {
    const link = document.createElement("a");
    link.href = result.path;
    link.textContent = "Open inbox";
    notice.append(" ", link);
  }
  const dismiss = document.createElement("button");
  dismiss.className = "bn-button";
  dismiss.textContent = "Dismiss";
  dismiss.addEventListener("click", () => notice.replaceChildren());
  notice.append(" ", dismiss);
  positionCaptureNotice(notice, origin);
};

const positionCaptureNotice = (notice: HTMLElement, origin: HTMLElement | null): void => {
  notice.removeAttribute("style");
  const anchor = origin?.getBoundingClientRect();
  if (!anchor || anchor.bottom < 0 || anchor.bottom > innerHeight - 120) return;
  const width = notice.getBoundingClientRect().width;
  notice.style.left = `${Math.max(12, Math.min(anchor.left, innerWidth - width - 12))}px`;
  notice.style.top = `${Math.max(12, anchor.bottom + 8)}px`;
  notice.style.bottom = "auto";
  notice.style.transform = "none";
};
