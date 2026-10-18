import { CaptureAttempts } from "./capture-attempts";
import { bindCaptureDraft } from "./capture-draft";

export const prepareEditorCapture = (
  button: HTMLButtonElement | null, input: HTMLTextAreaElement | null, vaultID: string
): { start(text: string): string; settled(): void } => {
  const root = button?.parentElement ?? document.createElement("div");
  root.dataset.captureVault = vaultID;
  root.dataset.captureTarget = "editor-capture";
  const saveDraft = input ? bindCaptureDraft(root, input) : () => {};
  const attempts = new CaptureAttempts(root);
  let submitted = "";
  return {
    start(text) { submitted = text; saveDraft(); return attempts.start(text); },
    settled() {
      if (input?.value.trim() === submitted) input.value = "";
      saveDraft();
      attempts.settled(submitted);
    },
  };
};
