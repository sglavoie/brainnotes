export type UndoToastOptions = {
  message: string;
  journalID: string;
  historyHref: string;
};

export type UndoToast = {
  element: HTMLElement;
  show: (options: UndoToastOptions) => void;
  hide: () => void;
  journalID: () => string;
};

export type UndoToastDeps = {
  onUndo: (journalID: string) => void;
};

// createUndoToast builds the one post-write affordance. It is announced
// politely rather than assertively so a screen reader finishes the status
// message before hearing that an undo is available. The live region wraps the
// toast and is never hidden: a region that appears in the same tick as its
// text is not announced.
export const createUndoToast = (
  doc: Document,
  host: HTMLElement,
  deps: UndoToastDeps
): UndoToast => {
  const region = doc.createElement("div");
  region.className = "bn-live-region";
  region.setAttribute("role", "status");
  region.setAttribute("aria-live", "polite");

  const element = doc.createElement("div");
  element.className = "properties-toast";
  element.setAttribute("data-property-toast", "");
  element.hidden = true;
  region.appendChild(element);

  const message = doc.createElement("p");
  message.className = "properties-toast-message";
  message.setAttribute("data-property-toast-message", "");
  element.appendChild(message);

  const actions = doc.createElement("div");
  actions.className = "properties-toast-actions";

  const undo = doc.createElement("button");
  undo.type = "button";
  undo.className = "bn-button properties-button properties-toast-undo";
  undo.setAttribute("data-property-toast-undo", "");
  undo.textContent = "Undo";
  actions.appendChild(undo);

  const history = doc.createElement("a");
  history.className = "properties-toast-history";
  history.setAttribute("data-property-toast-history", "");
  history.textContent = "View in history";
  actions.appendChild(history);

  const dismiss = doc.createElement("button");
  dismiss.type = "button";
  dismiss.className = "bn-button properties-button properties-toast-dismiss";
  dismiss.setAttribute("data-property-toast-dismiss", "");
  dismiss.textContent = "Dismiss";
  actions.appendChild(dismiss);

  element.appendChild(actions);
  host.appendChild(region);

  let entryID = "";

  const hide = (): void => {
    entryID = "";
    element.hidden = true;
    message.textContent = "";
  };

  undo.addEventListener("click", () => {
    const pending = entryID;
    if (pending === "") {
      return;
    }
    hide();
    deps.onUndo(pending);
  });
  dismiss.addEventListener("click", hide);

  return {
    element,
    show: (options: UndoToastOptions): void => {
      entryID = options.journalID;
      message.textContent = options.message;
      history.href = options.historyHref;
      undo.hidden = options.journalID === "";
      element.hidden = false;
    },
    hide,
    journalID: () => entryID,
  };
};
