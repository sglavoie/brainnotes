import { queryAllElements, queryElement } from "./dom";
import { setStatusText } from "./status";

type CopyStatusKind = "" | "is-success" | "is-error";
type WritableClipboard = Pick<Clipboard, "writeText">;

export type CopyPayload = {
  text: string;
  label: string;
};

export const readCopyPayload = (button: HTMLElement): CopyPayload => ({
  text: button.getAttribute("data-copy-text") || "",
  label: button.getAttribute("data-copy-label") || "Value",
});

export const copySuccessMessage = (label: string): string => `${label || "Value"} copied.`;

export const hasWritableClipboard = (
  clipboard: WritableClipboard | null | undefined
): clipboard is WritableClipboard =>
  Boolean(clipboard && typeof clipboard.writeText === "function");

const showCopyFallback = (root: HTMLElement, payload: CopyPayload): void => {
  let field = root.querySelector<HTMLTextAreaElement>("[data-copy-fallback]");
  if (!field) {
    field = document.createElement("textarea");
    field.dataset.copyFallback = "";
    field.className = "copy-fallback";
    field.readOnly = true;
    field.rows = 2;
    root.appendChild(field);
  }
  field.setAttribute("aria-label", `${payload.label} — copy manually`);
  field.value = payload.text;
  field.focus();
  field.select();
};

export const initCopyControls = (): void => {
  const roots = queryAllElements(document, "[data-copy-controls]", HTMLElement);
  if (roots.length === 0) {
    return;
  }

  const clipboard = navigator.clipboard as WritableClipboard | undefined;
  const canWriteClipboard = hasWritableClipboard(clipboard);

  roots.forEach((root) => {
    const statusEl = queryElement(root, "[data-copy-status]", HTMLElement);
    const buttons = queryAllElements(root, "[data-copy-text]", HTMLButtonElement);
    if (!statusEl || buttons.length === 0) {
      return;
    }

    const setStatus = (message: string, kind: CopyStatusKind = ""): void => {
      setStatusText(statusEl, message || " ", kind, ["is-success", "is-error"]);
    };

    buttons.forEach((button) => {
      button.addEventListener("click", async () => {
        const payload = readCopyPayload(button);
        if (!payload.text) {
          setStatus("Nothing to copy.", "is-error");
          return;
        }
        if (!canWriteClipboard) {
          setStatus("Clipboard unavailable. Copy the selected text below.", "is-error");
          showCopyFallback(root, payload);
          return;
        }

        button.disabled = true;
        try {
          await clipboard.writeText(payload.text);
          root.querySelector("[data-copy-fallback]")?.remove();
          setStatus(copySuccessMessage(payload.label), "is-success");
        } catch {
          setStatus("Copy failed. Copy the selected text below.", "is-error");
          showCopyFallback(root, payload);
        } finally {
          button.disabled = false;
        }
      });
    });
  });
};
