import {
  type PropertyDetailResult,
  type PropertyMutationAction,
  type PropertyRow,
  type PropertySchemaDefinitionInput,
  type PropertySchemaOp,
  type PropertySummary,
  type PropertyTypedValue,
} from "./api";
import { queryAllElements, queryElement } from "./dom";
import type { FetchLike } from "./properties-model";
import { createPreviewDialog } from "./properties-preview-dialog";
import { valueDisplayText } from "./properties-model";
import { renderRows } from "./properties-render";
import { createUndoToast } from "./properties-toast";
import { createValueControl, type ValueControl } from "./properties-value-control";
import {
  actionTitle,
  editAllowed,
  editItemKind,
  editKind,
  selectionLabel,
  typedValueFrom,
} from "./properties-workbench-model";
import { createWorkbenchEditor } from "./properties-workbench-editor";
import { createWorkbenchMutations } from "./properties-workbench-mutations";
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
  reload: () => Promise<void>;
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
  const fetcher = deps.fetch || fetch;
  const now = deps.now || (() => new Date());

  const editorEl = queryElement(host.root, "[data-property-editor]", HTMLElement);
  const countEl = queryElement(host.root, "[data-property-selection-count]", HTMLElement);
  const selectAllEl = queryElement(host.root, "[data-property-select-all]", HTMLButtonElement);
  const clearSelectionEl = queryElement(
    host.root,
    "[data-property-clear-selection]",
    HTMLButtonElement
  );
  const searchEl = queryElement(host.root, "[data-property-filter-query]", HTMLInputElement);
  const dialogHost =
    queryElement(host.root, "[data-property-dialog-host]", HTMLElement) || host.root;
  const toastHost =
    queryElement(host.root, "[data-property-toast-host]", HTMLElement) || host.root;

  const dialog = createPreviewDialog(doc, dialogHost, { now });
  let runUndo = async (_journalID: string): Promise<void> => {};
  const toast = createUndoToast(doc, toastHost, {
    onUndo: (journalID) => {
      void runUndo(journalID);
    },
  });
  const editor = createWorkbenchEditor(doc);

  const selected = new Set<string>();
  let summary: PropertySummary | null = null;
  let rows: PropertyRow[] = [];
  let matchValue: { kind: string; text: string } | null = null;
  let busy = false;

  const setStatus = (message: string, kind: StatusKind = ""): void => {
    host.setStatus(message, kind);
  };

  const rowElements = (): HTMLElement[] =>
    queryAllElements(host.rowsEl, "[data-property-row]", HTMLElement);

  const renderSelection = (): void => {
    if (countEl) {
      countEl.textContent = selectionLabel(selected.size);
    }
    if (clearSelectionEl) {
      clearSelectionEl.disabled = selected.size === 0;
    }
    for (const element of rowElements()) {
      const relPath = element.getAttribute("data-property-row") || "";
      element.classList.toggle("is-selected", selected.has(relPath));
    }
    for (const box of queryAllElements(
      host.rowsEl,
      "[data-property-row-select]",
      HTMLInputElement
    )) {
      box.checked = selected.has(box.getAttribute("data-property-row-select") || "");
    }
    renderActions();
  };

  const renderActions = (): void => {
    for (const button of queryAllElements(
      host.root,
      "[data-property-action]",
      HTMLButtonElement
    )) {
      const action = button.getAttribute("data-property-action") || "";
      const needsMatch = action === "replace_value";
      button.disabled =
        busy || selected.size === 0 || (needsMatch && matchValue === null);
    }
  };

  const setBusy = (next: boolean): void => {
    busy = next;
    host.root.setAttribute("aria-busy", next ? "true" : "false");
    renderActions();
  };

  const toggleSelection = (relPath: string, force?: boolean): void => {
    const next = force === undefined ? !selected.has(relPath) : force;
    if (next) {
      selected.add(relPath);
    } else {
      selected.delete(relPath);
    }
    renderSelection();
  };

  const focusRow = (index: number): void => {
    const elements = rowElements();
    if (elements.length === 0) {
      return;
    }
    const clamped = Math.max(0, Math.min(index, elements.length - 1));
    elements.forEach((element, position) => {
      element.tabIndex = position === clamped ? 0 : -1;
    });
    elements[clamped]?.focus();
  };

  // updateRowText repaints the edited row from what was just written, so the
  // value on screen is correct before the refetch that restates the totals.
  const updateRowText = (relPath: string, text: string): void => {
    const element = rowElements().find(
      (row) => row.getAttribute("data-property-row") === relPath
    );
    const value = element ? queryElement(element, ".properties-row-value", HTMLElement) : null;
    if (!value) {
      return;
    }
    value.textContent = text;
    value.classList.remove("is-missing");
  };

  const mutations = createWorkbenchMutations({
    clearSelection: () => {
      selected.clear();
      renderSelection();
    },
    closeEditor: editor.close,
    dialog,
    doc,
    fetcher,
    historyHref: host.historyHref,
    property: host.property,
    reload: host.reload,
    setBusy,
    setStatus,
    toast,
    token: host.token,
    updateRowText,
  });
  const { previewSchema, runImmediate, runPreview } = mutations;
  runUndo = mutations.runUndo;

  const buildValueControl = (
    form: HTMLFormElement,
    current: PropertySummary,
    seed?: PropertyTypedValue
  ): ValueControl => {
    const control = createValueControl(doc, {
      kind: editKind(current),
      items: editItemKind(current),
      allowed: editAllowed(current),
      value: seed,
      id: `property-editor-${host.property}`,
      label: `${host.property} value`,
    });
    form.appendChild(control.element);
    return control;
  };

  const openRowEditor = (relPath: string, op: "replace" | "list_add"): void => {
    const current = summary;
    const row = rows.find((candidate) => candidate.rel_path === relPath);
    const element = rowElements().find(
      (candidate) => candidate.getAttribute("data-property-row") === relPath
    );
    if (!row || !element || !current) {
      return;
    }
    const itemKind = editItemKind(current);
    const seed =
      op === "replace" ? typedValueFrom(row.value, editKind(current), itemKind) : undefined;

    editor.mount(
      element,
      op === "replace" ? `Edit ${host.property}` : `Add to ${host.property}`,
      "Save",
      (form) => {
        const control =
          op === "replace"
            ? buildValueControl(form, current, seed)
            : (() => {
                const single = createValueControl(doc, {
                  kind: itemKind,
                  items: itemKind,
                  allowed: editAllowed(current),
                  id: `property-editor-item-${host.property}`,
                  label: `${host.property} item`,
                });
                form.appendChild(single.element);
                return single;
              })();
        control.focus();
        return () => {
          const message = control.error();
          if (message !== "") {
            return message;
          }
          const value = control.read();
          const text =
            op === "replace"
              ? previewTextFor(value)
              : `${valueDisplayText(row.value)}, ${value.text}`;
          void runImmediate(relPath, op, value, row.hash, text);
          return null;
        };
      }
    );
  };

  const previewTextFor = (value: PropertyTypedValue): string =>
    value.kind === "list"
      ? (value.items || []).map((item) => item.text).join(", ")
      : value.text;

  const openActionEditor = (action: PropertyMutationAction): void => {
    const current = summary;
    const notes = [...selected];
    if (notes.length === 0) {
      setStatus("Select at least one note first.", "is-error");
      return;
    }

    if (action === "unset" || action === "repair") {
      void runPreview({ action, property: host.property, notes });
      return;
    }
    if (!editorEl || !current) {
      return;
    }

    editor.mount(editorEl, actionTitle(action, host.property), "Preview", (form) => {
      if (action === "rename_property") {
        const input = doc.createElement("input");
        input.type = "text";
        input.className = "properties-control properties-control-input";
        input.id = "property-editor-new-name";
        input.autocomplete = "off";
        input.spellcheck = false;
        input.setAttribute("aria-label", "New property name");
        input.setAttribute("data-property-new-name", "");
        input.value = host.property;
        form.appendChild(input);
        input.focus();
        return () => {
          const newName = input.value.trim();
          if (newName === "") {
            return "Enter a new property name.";
          }
          if (newName === host.property) {
            return "Enter a name different from the current one.";
          }
          void runPreview({
            action,
            property: host.property,
            new_name: newName,
            notes,
          });
          return null;
        };
      }

      const control = buildValueControl(form, current);
      control.focus();
      return () => {
        const message = control.error();
        if (message !== "") {
          return message;
        }
        const value = control.read();
        if (action === "replace_value") {
          if (!matchValue) {
            return "Choose the value to replace first.";
          }
          void runPreview({
            action,
            property: host.property,
            notes,
            match: { kind: matchValue.kind as PropertyTypedValue["kind"], text: matchValue.text },
            value,
          });
          return null;
        }
        void runPreview({ action, property: host.property, notes, value });
        return null;
      };
    });
  };

  host.rowsEl.addEventListener("change", (event) => {
    const target = event.target;
    if (!(target instanceof HTMLInputElement) || !target.hasAttribute("data-property-row-select")) {
      return;
    }
    toggleSelection(target.getAttribute("data-property-row-select") || "", target.checked);
  });

  host.rowsEl.addEventListener("click", (event) => {
    const target = event.target;
    if (!(target instanceof Element)) {
      return;
    }
    const edit = target.closest<HTMLButtonElement>("[data-property-row-edit]");
    if (edit && !edit.disabled) {
      openRowEditor(edit.getAttribute("data-property-row-edit") || "", "replace");
      return;
    }
    const add = target.closest<HTMLButtonElement>("[data-property-row-add]");
    if (add && !add.disabled) {
      openRowEditor(add.getAttribute("data-property-row-add") || "", "list_add");
      return;
    }
    const unset = target.closest<HTMLButtonElement>("[data-property-row-unset]");
    if (unset && !unset.disabled) {
      void runPreview({
        action: "unset",
        property: host.property,
        notes: [unset.getAttribute("data-property-row-unset") || ""],
      });
    }
  });

  host.rowsEl.addEventListener("keydown", (event) => {
    const target = event.target;
    if (!(target instanceof HTMLElement)) {
      return;
    }
    const row = target.closest<HTMLElement>("[data-property-row]");
    if (!row) {
      return;
    }
    const elements = rowElements();
    const index = elements.indexOf(row);
    if (event.key === "ArrowDown") {
      event.preventDefault();
      focusRow(index + 1);
      return;
    }
    if (event.key === "ArrowUp") {
      event.preventDefault();
      focusRow(index - 1);
      return;
    }
    if (target !== row) {
      return;
    }
    if (event.key === " ") {
      event.preventDefault();
      toggleSelection(row.getAttribute("data-property-row") || "");
      return;
    }
    if (event.key === "Enter") {
      event.preventDefault();
      openRowEditor(row.getAttribute("data-property-row") || "", "replace");
    }
  });

  host.root.addEventListener("click", (event) => {
    const target = event.target;
    if (!(target instanceof Element)) {
      return;
    }
    const button = target.closest<HTMLButtonElement>("[data-property-action]");
    if (!button || button.disabled) {
      return;
    }
    openActionEditor(button.getAttribute("data-property-action") as PropertyMutationAction);
  });

  selectAllEl?.addEventListener("click", () => {
    for (const row of rows) {
      selected.add(row.rel_path);
    }
    renderSelection();
  });
  clearSelectionEl?.addEventListener("click", () => {
    selected.clear();
    renderSelection();
  });

  doc.addEventListener("keydown", (event) => {
    if (dialog.isOpen()) {
      return;
    }
    if (event.key === "/" && searchEl) {
      const target = event.target;
      const typing =
        target instanceof HTMLInputElement ||
        target instanceof HTMLTextAreaElement ||
        target instanceof HTMLSelectElement;
      if (typing) {
        return;
      }
      event.preventDefault();
      searchEl.focus();
      searchEl.select();
      return;
    }
    if (event.key === "Escape") {
      if (editor.isOpen()) {
        editor.close();
        return;
      }
      if (selected.size > 0) {
        selected.clear();
        renderSelection();
        setStatus("Selection cleared.");
      }
    }
  });

  return {
    render: (result: PropertyDetailResult): void => {
      summary = result.property;
      rows = result.rows;
      const active = result.filter.value || "";
      const match = result.values.find(
        (entry) => entry.value.supported && entry.value.text === active
      );
      matchValue = match ? { kind: match.value.kind, text: match.value.text } : null;

      editor.reset();
      renderRows(host.rowsEl, result.rows, {
        selected,
        listKind: editKind(result.property) === "list",
      });
      const first = rowElements()[0];
      if (first) {
        first.tabIndex = 0;
      }
      renderSelection();
    },
    selection: () => [...selected],
    previewSchema,
  };
};
