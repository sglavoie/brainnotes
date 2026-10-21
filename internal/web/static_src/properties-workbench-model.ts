import type {
  PropertyKind,
  PropertyMutationAction,
  PropertyMutationResult,
  PropertyScalarKind,
  PropertySummary,
  PropertyTypedValue,
  PropertyValue,
} from "./api";
import { isPropertyKind, isPropertyScalarKind } from "./properties-value-control";

export type EditorMode = "replace" | "list_add";

// editKind picks the kind an editor should collect. A configured definition is
// authoritative; an unconfigured property falls back to its one observed kind,
// and to a string when the vault disagrees with itself.
export const editKind = (summary: PropertySummary): PropertyKind => {
  const declared = summary.definition?.type || "";
  if (isPropertyKind(declared)) {
    return declared;
  }
  const observed = summary.observed_kinds.filter((kind) => isPropertyKind(kind));
  return observed.length === 1 && observed[0] && isPropertyKind(observed[0])
    ? observed[0]
    : "string";
};

export const editItemKind = (summary: PropertySummary): PropertyScalarKind => {
  const declared = summary.definition?.items || "";
  if (isPropertyScalarKind(declared)) {
    return declared;
  }
  return "string";
};

export const editAllowed = (summary: PropertySummary): string[] =>
  summary.definition?.allowed ? [...summary.definition.allowed] : [];

// typedValueFrom renders a rendered value back into the transport form an edit
// sends. An unsupported value has no reproducible spelling, so it yields
// nothing and the editor starts empty rather than inventing text.
export const typedValueFrom = (
  value: PropertyValue | undefined,
  kind: PropertyKind,
  items: PropertyScalarKind
): PropertyTypedValue | undefined => {
  if (!value || !value.supported) {
    return undefined;
  }
  if (kind === "list") {
    const entries = value.items.length > 0 ? value.items : [value];
    return {
      kind: "list",
      text: "",
      items: entries
        .filter((entry) => entry.supported)
        .map((entry) => ({ kind: items, text: entry.text })),
    };
  }
  return { kind, text: value.text };
};

export const actionTitle = (action: PropertyMutationAction, property: string): string => {
  switch (action) {
    case "set":
      return `Set ${property}`;
    case "unset":
      return `Remove ${property}`;
    case "replace_value":
      return `Replace a value of ${property}`;
    case "rename_property":
      return `Rename ${property}`;
    case "repair":
      return `Repair ${property}`;
  }
};

export const actionConfirmLabel = (action: PropertyMutationAction): string => {
  switch (action) {
    case "set":
      return "Apply values";
    case "unset":
      return "Remove property";
    case "replace_value":
      return "Replace values";
    case "rename_property":
      return "Rename property";
    case "repair":
      return "Repair notes";
  }
};

export const selectionLabel = (count: number): string =>
  count === 0
    ? "No notes selected."
    : `${count} ${count === 1 ? "note" : "notes"} selected.`;

// mutationSuccessMessage reports what an applied plan actually wrote, so a plan
// that touched fewer notes than were selected cannot read as a full success.
export const mutationSuccessMessage = (result: PropertyMutationResult): string => {
  const notes = `${result.affected} ${result.affected === 1 ? "note" : "notes"}`;
  const config = result.config_diff.length > 0 ? " and brainnotes.toml" : "";
  return result.affected === 0 && config === ""
    ? "Nothing changed."
    : `Updated ${notes}${config}.`;
};

export const undoToastMessage = (result: PropertyMutationResult): string =>
  `${mutationSuccessMessage(result)} You can undo this change.`;
