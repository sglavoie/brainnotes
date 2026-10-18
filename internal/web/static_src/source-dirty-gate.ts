import type { StatusKind } from "./status";

// What stands between unsaved text and an operation that would move it out of
// reach.
//
// Every management action on the editor page goes through here, and the reason
// is that the editor's copy of a note is the only copy: the vault holds the
// bytes it was loaded from, the browser holds what has been typed since, and
// nothing reconciles them but a save. An action that rewrites the note under
// that — a property set, an undo — leaves the editor pinned to a revision the
// vault has moved past, and one that *renames* it leaves the kept draft filed
// under a note key that no longer exists, where nothing will ever offer it back.
//
// So the two are not the same refusal, and this gate does not pretend they are.
// `allowDirty` is what separates them: an operation that only changes the note's
// content may be taken with unsaved text on screen, because the text is still on
// screen afterwards and the next save is refused by the server's own revision
// check with both versions shown. An operation that changes the note's
// *identity* may not, because there is no version of it where the draft survives
// unattended — so the only ways past it are saving first and not doing it.

export type DirtyGateElements = {
  panel: HTMLElement | null;
  message: HTMLElement | null;
  save: HTMLButtonElement | null;
  proceed: HTMLButtonElement | null;
  cancel: HTMLButtonElement | null;
};

export type DirtyGateOptions = {
  elements: DirtyGateElements;
  dirty(): boolean;
  save(): Promise<void>;
  setStatus(message: string, kind: StatusKind): void;
  setBusy?(busy: boolean): void;
};

export type GuardedAction = {
  // label is the sentence fragment the panel and its buttons are written
  // around: "Rename this note", "Move it to Trash".
  label: string;
  allowDirty: boolean;
  run(): Promise<void>;
};

export type DirtyGate = {
  guard(action: GuardedAction): void;
  pending(): GuardedAction | null;
  dispose(): void;
};

export const createDirtyGate = (options: DirtyGateOptions): DirtyGate => {
  const { elements } = options;
  let held: GuardedAction | null = null;
  let saving = false;

  const hide = (): void => {
    held = null;
    if (elements.panel) {
      elements.panel.hidden = true;
    }
  };

  const show = (action: GuardedAction): void => {
    held = action;
    if (elements.message) {
      elements.message.textContent = gateMessage(action);
    }
    if (elements.proceed) {
      elements.proceed.hidden = !action.allowDirty;
      elements.proceed.textContent = `${action.label} anyway`;
    }
    if (elements.save) {
      elements.save.textContent = `Save the note, then ${lowerFirst(action.label)}`;
    }
    if (elements.panel) {
      elements.panel.hidden = false;
    }
    // The shared prompt may be above the control that requested this action.
    elements.save?.focus();
  };

  const take = (action: GuardedAction): void => {
    hide();
    void action.run();
  };

  const onSave = async (): Promise<void> => {
    const action = held;
    if (!action || saving) return;
    saving = true;
    options.setBusy?.(true);
    try {
      await options.save();
    } catch {
      if (held === action) options.setStatus("Save not confirmed. Check the editor's save status before continuing.", "is-error");
      return;
    } finally {
      saving = false;
      options.setBusy?.(false);
    }
    // Cancel or disposal invalidates the continuation, even if saving succeeded.
    if (held !== action) return;
    if (options.dirty()) {
      options.setStatus(
        `The note was not saved, so ${lowerFirst(action.label)} has not been done.`,
        "is-error"
      );
      return;
    }
    take(action);
  };

  const onProceed = (): void => {
    const action = held;
    if (!saving && action?.allowDirty) {
      take(action);
    }
  };

  const onCancel = (): void => {
    const action = held;
    hide();
    if (action) options.setStatus(cancellationMessage(action, saving), "");
  };

  elements.save?.addEventListener("click", onSave);
  elements.proceed?.addEventListener("click", onProceed);
  elements.cancel?.addEventListener("click", onCancel);
  hide();

  return {
    guard(action): void {
      if (saving) return;
      if (!options.dirty()) {
        take(action);
        return;
      }
      show(action);
    },
    pending: (): GuardedAction | null => held,
    dispose(): void {
      held = null;
      elements.save?.removeEventListener("click", onSave);
      elements.proceed?.removeEventListener("click", onProceed);
      elements.cancel?.removeEventListener("click", onCancel);
    },
  };
};

const cancellationMessage = (action: GuardedAction, saving: boolean): string => saving
  ? `${action.label}: canceled. The save is still in progress; this action will not run.`
  : `${action.label}: canceled. Nothing was changed.`;

// gateMessage says what has *not* happened first, then what the choice is. The
// order matters: somebody who has just pressed a button they now suspect was
// destructive reads the first clause and stops worrying, and only then reads the
// rest.
export const gateMessage = (action: GuardedAction): string => {
  const opening = `${action.label} has not been done. This editor is holding text that is not in the vault.`;
  return action.allowDirty
    ? `${opening} Saving first keeps both; going ahead anyway leaves your text here, and the note on disk will have moved on.`
    : `${opening} This one changes where the note lives, which is where your unsaved text is filed too — so it has to be saved first, or not done.`;
};

const lowerFirst = (value: string): string =>
  value === "" ? value : value.charAt(0).toLowerCase() + value.slice(1);
