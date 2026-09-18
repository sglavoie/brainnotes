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
  };

  const take = (action: GuardedAction): void => {
    hide();
    void action.run();
  };

  const onSave = (): void => {
    const action = held;
    if (!action) {
      return;
    }
    void options.save().then(() => {
      // Re-asked rather than assumed. A save can be refused — a stale revision,
      // a note that is gone, an answer nobody got — and taking the action on the
      // strength of having *tried* to save is how the text this gate exists to
      // protect gets left behind.
      if (options.dirty()) {
        options.setStatus(
          `The note was not saved, so ${lowerFirst(action.label)} has not been done.`,
          "is-error"
        );
        return;
      }
      take(action);
    });
  };

  const onProceed = (): void => {
    const action = held;
    if (action?.allowDirty) {
      take(action);
    }
  };

  const onCancel = (): void => {
    const action = held;
    hide();
    if (action) {
      options.setStatus(`${action.label}: canceled. Nothing was changed.`, "");
    }
  };

  elements.save?.addEventListener("click", onSave);
  elements.proceed?.addEventListener("click", onProceed);
  elements.cancel?.addEventListener("click", onCancel);
  hide();

  return {
    guard(action): void {
      if (!options.dirty()) {
        take(action);
        return;
      }
      show(action);
    },
    pending: (): GuardedAction | null => held,
    dispose(): void {
      elements.save?.removeEventListener("click", onSave);
      elements.proceed?.removeEventListener("click", onProceed);
      elements.cancel?.removeEventListener("click", onCancel);
    },
  };
};

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
