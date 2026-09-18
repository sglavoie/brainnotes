import {
  buildCaptureForm,
  buildCreateNoteForm,
  buildFrontmatterSetForm,
  buildFrontmatterUnsetForm,
  buildNoteKeyMutationForm,
  buildUndoForm,
  type UndoConflict,
  type UndoResult,
} from "./api";
import { asRecord } from "./api-internal";
import { historyConflictSummary } from "./history-panel";
import { setStatusText, type StatusKind } from "./status";
import { createDirtyGate, type DirtyGate } from "./source-dirty-gate";
import { editorURLForKey, postManagement, type ManageOutcome } from "./source-manage-client";
import type { SourceEditorConfig } from "./source-editor-elements";
import type { SourceManageElements } from "./source-editor-elements";

// The note's own management, reachable from the page that is editing it.
//
// Until now every one of these actions lived on the note page: to rename a note
// you were editing you left the editor, which is exactly the moment unsaved text
// is at its most fragile. Bringing them here removes that trip and takes on the
// obligation that came with it — each one now runs beside a buffer the vault has
// never seen, so each one goes through the dirty gate first. Nothing in this
// module writes anything without having asked it.

export type ManageHost = {
  key(): string;
  dirty(): boolean;
  // reload re-reads the note from the vault. It is called only when the editor
  // holds nothing unsaved, because it replaces what is on screen.
  reload(): Promise<void>;
  navigate(url: string): void;
  save(): Promise<void>;
  confirm(message: string): boolean;
};

export type SourceManageDeps = {
  elements: SourceManageElements;
  config: SourceEditorConfig;
  host: ManageHost;
  fetch?: typeof fetch;
};

// ManageAction is one button. `plan` answers the request to send or a sentence
// explaining why there is nothing to send; `after` decides what the page does
// with a success, which is the only part that differs between a rename and a
// pin.
type ManageAction = {
  button: HTMLButtonElement | null;
  label: string;
  // allowDirty is false for the three operations that change where the note
  // lives. See source-dirty-gate.ts for why that is not the same refusal as the
  // others.
  allowDirty: boolean;
  plan(): PlannedRequest | string;
  after(outcome: ManageOutcome): void;
  refused?(outcome: ManageOutcome): string | null;
};

type PlannedRequest = { url: string; body: URLSearchParams };

export const initSourceManage = (deps: SourceManageDeps): (() => void) => {
  const { elements, config, host } = deps;
  const setStatus = (message: string, kind: StatusKind = ""): void =>
    setStatusText(elements.status, message, kind);

  const gate = createDirtyGate({
    elements: elements.gate,
    dirty: host.dirty,
    save: host.save,
    setStatus,
  });

  const run = async (action: ManageAction): Promise<void> => {
    const planned = action.plan();
    if (typeof planned === "string") {
      setStatus(planned, "is-error");
      return;
    }
    setStatus(`${action.label}…`, "is-loading");
    try {
      const outcome = await postManagement({
        url: planned.url,
        body: planned.body,
        token: config.token,
        fetch: deps.fetch,
      });
      if (!outcome.ok) {
        setStatus(action.refused?.(outcome) ?? outcome.message, "is-error");
        return;
      }
      action.after(outcome);
    } catch {
      setStatus(`${action.label} could not be sent. Nothing was changed.`, "is-error");
    }
  };

  const settle = (message: string): void => {
    // The note on disk has changed. Re-reading it is right only when this editor
    // is holding nothing of its own — otherwise the reload would replace the
    // person's text with the vault's, which is the loss the gate let them accept
    // for the *note*, never for their buffer.
    if (host.dirty()) {
      setStatus(
        `${message} Your unsaved text is still here, and the note on disk has moved on — saving will show you both versions.`,
        "is-success"
      );
      return;
    }
    setStatus(message, "is-success");
    void host.reload();
  };

  const actions = buildActions(deps, { setStatus, settle });
  const unbinds = actions.map((action) => bindAction(action, gate, run));
  return (): void => {
    for (const unbind of unbinds) {
      unbind();
    }
    gate.dispose();
  };
};

const bindAction = (
  action: ManageAction,
  gate: DirtyGate,
  run: (action: ManageAction) => Promise<void>
): (() => void) => {
  const onClick = (): void =>
    gate.guard({
      label: action.label,
      allowDirty: action.allowDirty,
      run: () => run(action),
    });
  action.button?.addEventListener("click", onClick);
  return (): void => action.button?.removeEventListener("click", onClick);
};

type ActionContext = {
  setStatus(message: string, kind: StatusKind): void;
  settle(message: string): void;
};

const buildActions = (deps: SourceManageDeps, ctx: ActionContext): ManageAction[] => [
  ...identityActions(deps),
  ...contentActions(deps, ctx),
  ...vaultActions(deps, ctx),
];

// identityActions are the three that change where the note lives. Each ends the
// page: two by opening the editor on the note's new key, one by leaving for the
// vault index, because there is no note left to edit.
const identityActions = (deps: SourceManageDeps): ManageAction[] => {
  const { elements, config, host } = deps;
  const endpoints = config.manage;
  return [
    {
      button: elements.rename,
      label: "Rename this note",
      allowDirty: false,
      plan: () =>
        required(elements.renameTitle, "Type the note's new title first.", (title) => ({
          url: endpoints.rename,
          body: withField(buildNoteKeyMutationForm(keyed(config, host)), "title", title),
        })),
      after: (outcome) => host.navigate(editorURLForKey(endpoints.editor, outcome.key)),
    },
    {
      button: elements.move,
      label: "Move this note",
      allowDirty: false,
      plan: () =>
        required(elements.moveFolder, "Type the folder to move the note into first.", (folder) => ({
          url: endpoints.move,
          body: withField(buildNoteKeyMutationForm(keyed(config, host)), "folder", folder),
        })),
      after: (outcome) => host.navigate(editorURLForKey(endpoints.editor, outcome.key)),
    },
    {
      button: elements.trash,
      label: "Move this note to Trash",
      allowDirty: false,
      plan: () =>
        host.confirm("Move this note to Trash? It can be restored from the history panel.")
          ? { url: endpoints.trash, body: buildNoteKeyMutationForm(keyed(config, host)) }
          : "Move to Trash: canceled. Nothing was changed.",
      after: () => host.navigate(endpoints.home),
    },
  ];
};

// contentActions rewrite this note's bytes behind the editor's back, so each of
// them ends in `settle`, which re-reads the note only when there is nothing
// unsaved to lose by doing so.
const contentActions = (deps: SourceManageDeps, ctx: ActionContext): ManageAction[] => {
  const { elements, config, host } = deps;
  const endpoints = config.manage;
  return [
    {
      button: elements.propertySet,
      label: "Set this property",
      allowDirty: true,
      plan: () =>
        required(elements.propertyName, "Name the property to set first.", (property) => ({
          url: endpoints.frontmatterSet,
          body: buildFrontmatterSetForm({
            ...keyed(config, host),
            property,
            op: "=",
            value: elements.propertyValue?.value ?? "",
          }),
        })),
      after: (outcome) => ctx.settle(outcome.message),
    },
    {
      button: elements.propertyUnset,
      label: "Remove this property",
      allowDirty: true,
      plan: () =>
        required(elements.propertyName, "Name the property to remove first.", (property) => ({
          url: endpoints.frontmatterUnset,
          body: buildFrontmatterUnsetForm({ ...keyed(config, host), property }),
        })),
      after: (outcome) => ctx.settle(outcome.message),
    },
    {
      button: elements.undo,
      label: "Undo the last change",
      allowDirty: true,
      plan: () =>
        host.confirm("Undo the most recent change to this vault?")
          ? { url: endpoints.undo, body: buildUndoForm({ token: config.token }) }
          : "Undo: canceled. Nothing was changed.",
      after: (outcome) => ctx.settle(outcome.message),
      refused: undoRefusal,
    },
  ];
};

// vaultActions touch the vault without touching this note's bytes, so none of
// them reloads. They are still gated: a pin and a property write both pin the
// configuration a save is planned against, so taking one under an unsaved edit
// is what turns the next save into a refusal nobody can act on.
const vaultActions = (deps: SourceManageDeps, ctx: ActionContext): ManageAction[] => {
  const { elements, config, host } = deps;
  const endpoints = config.manage;
  return [
    {
      button: elements.pin,
      label: "Pin this note",
      allowDirty: true,
      plan: () => ({ url: endpoints.pinSet, body: buildNoteKeyMutationForm(keyed(config, host)) }),
      after: (outcome) => ctx.setStatus(outcome.message, "is-success"),
    },
    {
      button: elements.unpin,
      label: "Unpin this note",
      allowDirty: true,
      plan: () => ({ url: endpoints.pinRemove, body: buildNoteKeyMutationForm(keyed(config, host)) }),
      after: (outcome) => ctx.setStatus(outcome.message, "is-success"),
    },
    {
      button: elements.capture,
      label: "Capture this line",
      allowDirty: true,
      plan: () =>
        required(elements.captureText, "Type the line to capture first.", (text) => ({
          url: endpoints.capture,
          body: buildCaptureForm({ text, token: config.token }),
        })),
      after: (outcome) => {
        clear(elements.captureText);
        // The captured bullet is echoed rather than reconstructed: the timestamp
        // in it came from the server's clock.
        ctx.setStatus(outcome.detail ? `${outcome.message} ${outcome.detail}` : outcome.message, "is-success");
      },
    },
    {
      button: elements.createNote,
      label: "Create this note",
      allowDirty: true,
      plan: () =>
        required(elements.createTarget, "Name the note to create first.", (target) => ({
          url: endpoints.create,
          body: buildCreateNoteForm({ ...keyed(config, host), target }),
        })),
      after: (outcome) => {
        clear(elements.createTarget);
        ctx.setStatus(`${outcome.message} ${outcome.relPath}`.trim(), "is-success");
      },
    },
  ];
};

// undoRefusal turns the one refusal with a remedy into the sentence that names
// it. A 409 from /api/undo means the paths on disk are not what the entry
// expected, and the paths themselves are what a person needs to see — the same
// disclosure the history panel makes, through the same function, because two
// wordings of one conflict is two things to keep true.
const undoRefusal = (outcome: ManageOutcome): string | null => {
  const result = asRecord(outcome.payload) as UndoResult | null;
  if (outcome.status !== 409 || !result?.requires_force) {
    return null;
  }
  const conflicts: UndoConflict[] = Array.isArray(result.conflicts) ? result.conflicts : [];
  return `${historyConflictSummary(conflicts)} Nothing was restored; the history panel can force it.`;
};

const keyed = (config: SourceEditorConfig, host: ManageHost): { key: string; token: string } => ({
  key: host.key() || config.key,
  token: config.token,
});

// required reads a text input and refuses an empty one with a sentence rather
// than posting a blank field for the server to reject in its own vocabulary.
const required = (
  input: HTMLInputElement | HTMLTextAreaElement | null,
  refusal: string,
  plan: (value: string) => PlannedRequest
): PlannedRequest | string => {
  const value = (input?.value ?? "").trim();
  return value === "" ? refusal : plan(value);
};

const withField = (body: URLSearchParams, name: string, value: string): URLSearchParams => {
  body.set(name, value);
  return body;
};

const clear = (input: HTMLInputElement | HTMLTextAreaElement | null): void => {
  if (input) {
    input.value = "";
  }
};
