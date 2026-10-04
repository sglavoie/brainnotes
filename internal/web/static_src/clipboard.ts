import { queryAllElements, queryElement } from "./dom";
import { setStatusText } from "./status";

type CopyStatusKind = "" | "is-success" | "is-error";
type WritableClipboard = Pick<Clipboard, "writeText">;
type ClipboardWriter = (text: string) => Promise<void>;

export type CopyFeedback = { message: string; kind: CopyStatusKind };

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

export const copyWithFallback = async (
  root: HTMLElement, payload: CopyPayload, write?: ClipboardWriter,
  isCurrent: () => boolean = () => true
): Promise<CopyFeedback> => {
  if (!payload.text) return { message: "Nothing to copy.", kind: "is-error" };
  let message = "Clipboard unavailable. Copy the selected text below.";
  if (write) {
    try {
      await write(payload.text);
      if (!isCurrent()) return { message: "", kind: "" };
      root.querySelector("[data-copy-fallback]")?.remove();
      return { message: copySuccessMessage(payload.label), kind: "is-success" };
    } catch {
      message = "Copy failed. Copy the selected text below.";
    }
  }
  if (!isCurrent()) return { message: "", kind: "" };
  showCopyFallback(root, payload);
  return { message, kind: "is-error" };
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
        const isCurrent = (): boolean => button.isConnected && readCopyPayload(button).text === payload.text;
        button.disabled = true;
        try {
          const feedback = await copyWithFallback(root, payload,
            canWriteClipboard ? (text) => clipboard.writeText(text) : undefined, isCurrent);
          if (isCurrent()) setStatus(feedback.message, feedback.kind);
        } finally {
          button.disabled = !readCopyPayload(button).text;
        }
      });
    });
  });
};
