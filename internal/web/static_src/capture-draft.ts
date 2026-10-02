// Session storage keeps the unfinished thought in this tab across navigation.
// Include the server's opaque vault ID: an origin can serve another vault later.
export const bindCaptureDraft = (
  root: HTMLElement,
  input: HTMLTextAreaElement
): (() => void) => {
  const vault = root.dataset.captureVault;
  if (!vault) {
    return () => {};
  }
  const key = `bn.capture-draft.v1:${JSON.stringify([
    vault, root.dataset.captureTarget, root.dataset.captureHeading,
  ])}`;
  const status = root.querySelector<HTMLElement>("[data-capture-draft-status]");
  const report = (message: string): void => {
    if (status) {
      status.textContent = message;
      status.hidden = message === "";
    }
  };
  const save = (): void => {
    try {
      if (input.value === "") {
        window.sessionStorage.removeItem(key);
      } else {
        window.sessionStorage.setItem(key, input.value);
      }
      report("");
    } catch {
      report(input.value === ""
        ? "The browser could not clear the old capture draft. It may reappear after a reload."
        : "The browser could not keep this draft. Keep this page open until you capture it.");
    }
  };
  try {
    const draft = window.sessionStorage.getItem(key);
    if (draft && input.value === "") {
      input.value = draft;
      report("Unfinished capture restored in this tab.");
    }
  } catch {
    report("Draft recovery is unavailable in this browser.");
  }
  input.addEventListener("input", save);
  return save;
};
