import {
  type PropertyDetailResult,
  type PropertySchemaDefinitionInput,
  type PropertySchemaOp,
} from "./api";
import { queryElement } from "./dom";
import type { FetchLike } from "./properties-model";
import { createPreviewDialog } from "./properties-preview-dialog";
import { renderRows } from "./properties-render";
import { createUndoToast } from "./properties-toast";
import { editKind } from "./properties-workbench-model";
import { createWorkbenchEditor } from "./properties-workbench-editor";
import { bindWorkbenchEvents } from "./properties-workbench-events";
import { openActionEditor, openRowEditor } from "./properties-workbench-forms";
import { createWorkbenchMutations } from "./properties-workbench-mutations";
import { createSelectionView, createWorkbenchState } from "./properties-workbench-selection";
import type { StatusKind } from "./status";

export type WorkbenchDeps = {
  fetch?: FetchLike;
  now?: () => Date;
};

export type WorkbenchHost = {
  root: HTMLElement;
  rowsEl: HTMLElement;
  property: string;
  token: string;
  historyHref: string;
  reload: () => Promise<boolean>;
  setStatus: (message: string, kind: StatusKind) => void;
};

export type PropertyWorkbench = {
  render: (result: PropertyDetailResult) => void;
  selection: () => string[];
  previewSchema: (
    op: PropertySchemaOp,
    definition?: PropertySchemaDefinitionInput
  ) => Promise<void>;
};

// createPropertyWorkbench owns every write the property page can make. It is
// built only when the page carries a CSRF token, so a read-only render never
// grows a control the server would refuse.
export const createPropertyWorkbench = (
  host: WorkbenchHost,
  deps: WorkbenchDeps = {}
): PropertyWorkbench => {
  const doc = host.root.ownerDocument;
  const now = deps.now || (() => new Date());

  const editorEl = queryElement(host.root, "[data-property-editor]", HTMLElement);
  const dialogHost =
    queryElement(host.root, "[data-property-dialog-host]", HTMLElement) || host.root;
  const toastHost =
    queryElement(host.root, "[data-property-toast-host]", HTMLElement) || host.root;

  const dialog = createPreviewDialog(doc, dialogHost, { now });
  const undo: { run: (journalID: string) => Promise<void> } = { run: async () => {} };
  const toast = createUndoToast(doc, toastHost, {
    onUndo: (journalID) => {
      void undo.run(journalID);
    },
  });
  const editor = createWorkbenchEditor(doc);
  const state = createWorkbenchState();
  const view = createSelectionView(host.root, host.rowsEl, state);

  const setStatus = (message: string, kind: StatusKind = ""): void => {
    host.setStatus(message, kind);
  };

  const mutations = createWorkbenchMutations({
    clearSelection: () => {
      state.selected.clear();
      view.renderSelection();
    },
    closeEditor: editor.close,
    dialog,
    doc,
    fetcher: deps.fetch || fetch,
    historyHref: host.historyHref,
    property: host.property,
    reload: host.reload,
    setBusy: view.setBusy,
    setStatus,
    toast,
    token: host.token,
    updateRowText: view.updateRowText,
  });
  undo.run = mutations.runUndo;

  const formContext = {
    doc,
    property: host.property,
    state,
    editor,
    mutations,
    rowElements: view.rowElements,
    setStatus,
  };

  bindWorkbenchEvents({
    root: host.root,
    rowsEl: host.rowsEl,
    property: host.property,
    state,
    view,
    editor,
    dialog,
    runPreview: mutations.runPreview,
    openRowEditor: (relPath, op) => openRowEditor(formContext, relPath, op),
    openActionEditor: (action) => openActionEditor(formContext, editorEl, action),
    setStatus,
  });

  return {
    render: (result: PropertyDetailResult): void => {
      state.summary = result.property;
      state.rows = result.rows;
      const active = result.filter.value || "";
      const match = result.values.find(
        (entry) => entry.value.supported && entry.value.text === active
      );
      state.matchValue = match ? { kind: match.value.kind, text: match.value.text } : null;

      editor.reset();
      renderRows(host.rowsEl, result.rows, {
        selected: state.selected,
        listKind: editKind(result.property) === "list",
      });
      const first = view.rowElements()[0];
      if (first) {
        first.tabIndex = 0;
      }
      view.renderSelection();
      editor.refocus();
    },
    selection: () => [...state.selected],
    previewSchema: mutations.previewSchema,
  };
};
