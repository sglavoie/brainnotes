import { focusFallback, focusIsLost } from "./dom";

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
  // statusTarget names where focus goes after Undo hides the toast: the status
  // line that reports the undo's progress.
  statusTarget?: () => HTMLElement | null;
};

// triggerFrom remembers the control that caused the toast, so Dismiss can hand
// focus back to it; a press inside the toast itself is not a trigger.
const triggerFrom = (doc: Document, toast: HTMLElement): HTMLElement | null => {
  const active = doc.activeElement;
  return active instanceof HTMLElement && active !== doc.body && !toast.contains(active) ? active : null;
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
  let trigger: HTMLElement | null = null;

  const hide = (): void => {
    entryID = "";
    element.hidden = true;
    message.textContent = "";
  };

  // Hiding the toast removes the focused button. Undo lands on its progress:
  // the toast's own message when it stays up, else the page's status line.
  const focusAfterUndo = (): void => {
    if (!focusIsLost(doc)) return;
    if (!element.hidden) focusFallback(message);
    else focusFallback(deps.statusTarget?.() ?? (trigger?.isConnected ? trigger : null));
  };

  undo.addEventListener("click", () => {
    const pending = entryID;
    if (pending === "") {
      return;
    }
    hide();
    deps.onUndo(pending);
    focusAfterUndo();
  });
  dismiss.addEventListener("click", () => {
    hide();
    if (focusIsLost(doc) && trigger?.isConnected) trigger.focus();
  });

  return {
    element,
    show: (options: UndoToastOptions): void => {
      trigger = triggerFrom(doc, element) ?? trigger;
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
