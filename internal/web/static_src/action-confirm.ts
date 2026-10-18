export type ConfirmAction = (message: string, returnFocus?: Element | null) => boolean | Promise<boolean>;

type Confirmation = { title: string; description: string; confirmLabel: string; danger?: boolean };

// One native dialog owns keyboard containment, Escape, and focus restoration.
// Vault-authored names and paths are assigned as text, never markup.
export const confirmAction = (
  options: Confirmation, doc = document, returnFocus: Element | null = doc.activeElement,
): Promise<boolean> =>
  new Promise((resolve) => {
    if (doc.querySelector("dialog[open]")) { resolve(false); return; }
    const dialog = doc.createElement("dialog");
    dialog.className = "properties-dialog history-confirm";
    dialog.setAttribute("aria-labelledby", "history-confirm-heading");
    dialog.setAttribute("aria-describedby", "action-confirm-description");
    const heading = doc.createElement("h2");
    heading.id = "history-confirm-heading";
    heading.textContent = options.title;
    const description = doc.createElement("pre");
    description.id = "action-confirm-description";
    description.className = "history-confirm-description";
    description.textContent = options.description;
    const actions = doc.createElement("div");
    actions.className = "history-actions";
    const cancel = doc.createElement("button");
    cancel.className = "bn-button history-button";
    cancel.textContent = "Cancel";
    cancel.autofocus = true;
    const confirm = doc.createElement("button");
    confirm.className = options.danger === false ? "bn-button history-button" : "bn-button history-button history-danger";
    confirm.textContent = options.confirmLabel;
    cancel.addEventListener("click", () => dialog.close("cancel"));
    confirm.addEventListener("click", () => dialog.close("confirm"));
    dialog.addEventListener("close", () => {
      const accepted = dialog.returnValue === "confirm";
      dialog.remove();
      resolve(accepted);
      // Callers re-enable controls when their awaited confirmation settles.
      doc.defaultView?.setTimeout(() => {
        if (doc.activeElement === doc.body && returnFocus instanceof HTMLElement && returnFocus.isConnected) returnFocus.focus();
      }, 0);
    }, { once: true });
    actions.append(cancel, confirm);
    dialog.append(heading, description, actions);
    doc.body.append(dialog);
    dialog.showModal();
  });

