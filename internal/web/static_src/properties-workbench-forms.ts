import type { PropertyMutationAction, PropertySummary, PropertyTypedValue } from "./api";
import { valueDisplayText } from "./properties-model";
import { createValueControl, type ValueControl } from "./properties-value-control";
import type { WorkbenchEditor } from "./properties-workbench-editor";
import {
  actionTitle,
  editAllowed,
  editItemKind,
  editKind,
  typedValueFrom,
} from "./properties-workbench-model";
import type { WorkbenchMutations } from "./properties-workbench-mutations";
import type { WorkbenchState } from "./properties-workbench-selection";
import type { StatusKind } from "./status";

export type WorkbenchFormContext = {
  doc: Document;
  property: string;
  state: WorkbenchState;
  editor: WorkbenchEditor;
  mutations: Pick<WorkbenchMutations, "runImmediate" | "runPreview">;
  rowElements: () => HTMLElement[];
  setStatus: (message: string, kind?: StatusKind) => void;
};

type Submit = () => string | null;

const previewTextFor = (value: PropertyTypedValue): string =>
  value.kind === "list" ? (value.items || []).map((item) => item.text).join(", ") : value.text;

const buildValueControl = (
  ctx: WorkbenchFormContext,
  form: HTMLFormElement,
  current: PropertySummary,
  seed?: PropertyTypedValue
): ValueControl => {
  const control = createValueControl(ctx.doc, {
    kind: editKind(current),
    items: editItemKind(current),
    allowed: editAllowed(current),
    value: seed,
    id: `property-editor-${ctx.property}`,
    label: `${ctx.property} value`,
  });
  form.appendChild(control.element);
  return control;
};

const buildItemControl = (
  ctx: WorkbenchFormContext,
  form: HTMLFormElement,
  current: PropertySummary
): ValueControl => {
  const itemKind = editItemKind(current);
  const single = createValueControl(ctx.doc, {
    kind: itemKind,
    items: itemKind,
    allowed: editAllowed(current),
    id: `property-editor-item-${ctx.property}`,
    label: `${ctx.property} item`,
  });
  form.appendChild(single.element);
  return single;
};

export const openRowEditor = (
  ctx: WorkbenchFormContext,
  relPath: string,
  op: "replace" | "list_add"
): void => {
  const current = ctx.state.summary;
  const row = ctx.state.rows.find((candidate) => candidate.rel_path === relPath);
  const element = ctx
    .rowElements()
    .find((candidate) => candidate.getAttribute("data-property-row") === relPath);
  if (!row || !element || !current) {
    return;
  }
  const seed =
    op === "replace"
      ? typedValueFrom(row.value, editKind(current), editItemKind(current))
      : undefined;

  ctx.editor.mount(
    element,
    op === "replace" ? `Edit ${ctx.property}` : `Add to ${ctx.property}`,
    "Save",
    (form): Submit => {
      const control =
        op === "replace"
          ? buildValueControl(ctx, form, current, seed)
          : buildItemControl(ctx, form, current);
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
        void ctx.mutations.runImmediate(relPath, op, value, row.hash, text);
        return null;
      };
    }
  );
};

const buildRenameForm = (
  ctx: WorkbenchFormContext,
  form: HTMLFormElement,
  notes: string[]
): Submit => {
  const input = ctx.doc.createElement("input");
  input.type = "text";
  input.className = "properties-control properties-control-input";
  input.id = "property-editor-new-name";
  input.autocomplete = "off";
  input.spellcheck = false;
  input.setAttribute("aria-label", "New property name");
  input.setAttribute("data-property-new-name", "");
  input.value = ctx.property;
  form.appendChild(input);
  input.focus();
  return () => {
    const newName = input.value.trim();
    if (newName === "") {
      return "Enter a new property name.";
    }
    if (newName === ctx.property) {
      return "Enter a name different from the current one.";
    }
    void ctx.mutations.runPreview({
      action: "rename_property",
      property: ctx.property,
      new_name: newName,
      notes,
    });
    return null;
  };
};

const buildValueActionForm = (
  ctx: WorkbenchFormContext,
  form: HTMLFormElement,
  action: PropertyMutationAction,
  notes: string[],
  current: PropertySummary
): Submit => {
  const control = buildValueControl(ctx, form, current);
  control.focus();
  return () => {
    const message = control.error();
    if (message !== "") {
      return message;
    }
    const value = control.read();
    const match = ctx.state.matchValue;
    if (action !== "replace_value") {
      void ctx.mutations.runPreview({ action, property: ctx.property, notes, value });
      return null;
    }
    if (!match) {
      return "Choose the value to replace first.";
    }
    void ctx.mutations.runPreview({
      action,
      property: ctx.property,
      notes,
      match: { kind: match.kind as PropertyTypedValue["kind"], text: match.text },
      value,
    });
    return null;
  };
};

export const openActionEditor = (
  ctx: WorkbenchFormContext,
  editorEl: HTMLElement | null,
  action: PropertyMutationAction
): void => {
  const current = ctx.state.summary;
  const notes = [...ctx.state.selected];
  if (notes.length === 0) {
    ctx.setStatus("Select at least one note first.", "is-error");
    return;
  }

  if (action === "unset" || action === "repair") {
    void ctx.mutations.runPreview({ action, property: ctx.property, notes });
    return;
  }
  if (!editorEl || !current) {
    return;
  }

  ctx.editor.mount(editorEl, actionTitle(action, ctx.property), "Preview", (form): Submit =>
    action === "rename_property"
      ? buildRenameForm(ctx, form, notes)
      : buildValueActionForm(ctx, form, action, notes, current)
  );
};
