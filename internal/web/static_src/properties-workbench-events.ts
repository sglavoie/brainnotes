import type { PropertyMutationAction } from "./api";
import { queryElement } from "./dom";
import { isEditableTarget, isSearchShortcut } from "./search-shortcut";
import type { WorkbenchEditor } from "./properties-workbench-editor";
import type { WorkbenchMutations } from "./properties-workbench-mutations";
import type { SelectionView, WorkbenchState } from "./properties-workbench-selection";
import type { StatusKind } from "./status";

export type WorkbenchEventContext = {
  root: HTMLElement;
  rowsEl: HTMLElement;
  property: string;
  state: WorkbenchState;
  view: SelectionView;
  editor: WorkbenchEditor;
  dialog: { isOpen: () => boolean };
  runPreview: WorkbenchMutations["runPreview"];
  openRowEditor: (relPath: string, op: "replace" | "list_add") => void;
  openActionEditor: (action: PropertyMutationAction) => void;
  setStatus: (message: string, kind?: StatusKind) => void;
};

const attr = (element: Element, name: string): string => element.getAttribute(name) || "";

const bindRowClicks = (ctx: WorkbenchEventContext): void => {
  ctx.rowsEl.addEventListener("click", (event) => {
    const target = event.target;
    if (!(target instanceof Element)) {
      return;
    }
    const edit = target.closest<HTMLButtonElement>("[data-property-row-edit]");
    if (edit && !edit.disabled) {
      ctx.openRowEditor(attr(edit, "data-property-row-edit"), "replace");
      return;
    }
    const add = target.closest<HTMLButtonElement>("[data-property-row-add]");
    if (add && !add.disabled) {
      ctx.openRowEditor(attr(add, "data-property-row-add"), "list_add");
      return;
    }
    const unset = target.closest<HTMLButtonElement>("[data-property-row-unset]");
    if (unset && !unset.disabled) {
      void ctx.runPreview({
        action: "unset",
        property: ctx.property,
        notes: [attr(unset, "data-property-row-unset")],
      });
    }
  });
};

const bindRowKeys = (ctx: WorkbenchEventContext): void => {
  ctx.rowsEl.addEventListener("keydown", (event) => {
    const target = event.target;
    if (!(target instanceof HTMLElement)) {
      return;
    }
    const row = target.closest<HTMLElement>("[data-property-row]");
    if (!row) {
      return;
    }
    const index = ctx.view.rowElements().indexOf(row);
    if (event.key === "ArrowDown") {
      event.preventDefault();
      ctx.view.focusRow(index + 1);
      return;
    }
    if (event.key === "ArrowUp") {
      event.preventDefault();
      ctx.view.focusRow(index - 1);
      return;
    }
    if (target !== row) {
      return;
    }
    if (event.key === " ") {
      event.preventDefault();
      ctx.view.toggleSelection(attr(row, "data-property-row"));
      return;
    }
    if (event.key === "Enter") {
      event.preventDefault();
      ctx.openRowEditor(attr(row, "data-property-row"), "replace");
    }
  });
};

const bindRowSelection = (ctx: WorkbenchEventContext): void => {
  ctx.rowsEl.addEventListener("change", (event) => {
    const target = event.target;
    if (!(target instanceof HTMLInputElement) || !target.hasAttribute("data-property-row-select")) {
      return;
    }
    ctx.view.toggleSelection(attr(target, "data-property-row-select"), target.checked);
  });
};

const bindToolbar = (ctx: WorkbenchEventContext): void => {
  const { state, view } = ctx;
  ctx.root.addEventListener("click", (event) => {
    const target = event.target;
    if (!(target instanceof Element)) {
      return;
    }
    const button = target.closest<HTMLButtonElement>("[data-property-action]");
    if (!button || button.disabled) {
      return;
    }
    ctx.openActionEditor(attr(button, "data-property-action") as PropertyMutationAction);
  });

  queryElement(ctx.root, "[data-property-select-all]", HTMLButtonElement)?.addEventListener(
    "click",
    () => {
      for (const row of state.rows) {
        state.selected.add(row.rel_path);
      }
      view.renderSelection();
    }
  );
  queryElement(ctx.root, "[data-property-clear-selection]", HTMLButtonElement)?.addEventListener(
    "click",
    () => {
      state.selected.clear();
      view.renderSelection();
    }
  );
};

const isPlainEscape = (event: KeyboardEvent): boolean =>
  event.key === "Escape" && !event.ctrlKey && !event.metaKey && !event.altKey &&
  !event.shiftKey && !event.isComposing;

// Runs after controls inside the page, the Workspace popover and the header
// search have had the key: whichever of them used it first keeps it.
const bindDocumentKeys = (ctx: WorkbenchEventContext): void => {
  const searchEl = queryElement(ctx.root, "[data-property-filter-query]", HTMLInputElement);
  ctx.root.ownerDocument.addEventListener("keydown", (event) => {
    if (event.defaultPrevented || ctx.dialog.isOpen()) {
      return;
    }
    if (isSearchShortcut(event) && searchEl) {
      if (isEditableTarget(event.target)) {
        return;
      }
      event.preventDefault();
      searchEl.focus();
      searchEl.select();
      return;
    }
    if (!isPlainEscape(event)) {
      return;
    }
    if (ctx.editor.isOpen()) {
      ctx.editor.close();
      return;
    }
    if (ctx.state.selected.size > 0) {
      ctx.state.selected.clear();
      ctx.view.renderSelection();
      ctx.setStatus("Selection cleared.");
    }
  });
};

export const bindWorkbenchEvents = (ctx: WorkbenchEventContext): void => {
  bindRowSelection(ctx);
  bindRowClicks(ctx);
  bindRowKeys(ctx);
  bindToolbar(ctx);
  bindDocumentKeys(ctx);
};
