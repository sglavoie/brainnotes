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
  const scheduler = createPreviewScheduler({
    delayMs,
    render,
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
  const onRetry = (): void => {
    if (retry?.getAttribute("aria-disabled") !== "true") scheduler.flush(content());
  };
  retry?.addEventListener("click", onRetry);
  return {
    schedule: scheduler.schedule,
    flush: scheduler.flush,
    dispose: () => {
      retry?.removeEventListener("click", onRetry);
      scheduler.dispose();
    },
  };
};
