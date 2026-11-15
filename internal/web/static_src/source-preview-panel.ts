import type { SourceEditorElements } from "./source-editor-elements";
import { createPreviewScheduler, type PreviewScheduler, type PreviewRenderer } from "./source-preview";
import { setStatusText } from "./status";

export const createPreviewPanel = (
  elements: SourceEditorElements,
  render: PreviewRenderer,
  content: () => string,
  delayMs?: number
): PreviewScheduler => {
  const retry = elements.panel.querySelector<HTMLButtonElement>("[data-source-preview-retry]");
  let hasPreview = false;
  let latest: string | null = null;
  const pane = elements.preview?.closest<HTMLElement>('[data-source-pane="preview"]');
  // Let the rendered CSS layout decide visibility, including split view and
  // responsive tabs. Without observation, retain the eager-render fallback.
  const enabled = (): boolean => !pane || typeof ResizeObserver === "undefined" || pane.getClientRects().length > 0;
  const scheduler = createPreviewScheduler({
    delayMs,
    render,
    enabled,
    sink: {
      render: (html) => {
        if (elements.preview) elements.preview.innerHTML = html;
        hasPreview = true;
      },
      pending: () => {
        retry?.setAttribute("aria-disabled", "true");
        setStatusText(elements.previewStatus, "Rendering…", "is-loading");
      },
      settled: (message, failed) => {
        setStatusText(elements.previewStatus, failed
          ? `${message} ${hasPreview ? "The previous preview is outdated." : "No preview is available."}`
          : "Preview of the unsaved text.", failed ? "is-error" : "");
        if (!retry) return;
        if (!failed && retry.ownerDocument.activeElement === retry) {
          retry.closest<HTMLElement>('[data-source-pane="preview"]')?.focus();
        }
        retry.hidden = !failed;
        retry.removeAttribute("aria-disabled");
      },
    },
  });
  let visible = enabled();
  const observer = pane && typeof ResizeObserver !== "undefined" ? new ResizeObserver(() => {
    const shown = enabled();
    if (shown && !visible && latest !== null) scheduler.flush(latest);
    visible = shown;
  }) : null;
  if (pane) observer?.observe(pane);
  const onRetry = (): void => {
    if (retry?.getAttribute("aria-disabled") !== "true") scheduler.flush(content());
  };
  retry?.addEventListener("click", onRetry);
  return {
    schedule: (text) => { latest = text; scheduler.schedule(text); },
    flush: (text) => { latest = text; scheduler.flush(text); },
    dispose: () => {
      retry?.removeEventListener("click", onRetry);
      observer?.disconnect();
      scheduler.dispose();
    },
  };
};
