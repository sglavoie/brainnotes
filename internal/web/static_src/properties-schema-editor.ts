import type {
  PropertyKind,
  PropertyScalarKind,
  PropertySchemaDefinitionInput,
  PropertySchemaOp,
  PropertySummary,
} from "./api";
import { queryElement } from "./dom";
import {
  PROPERTY_KINDS,
  PROPERTY_SCALAR_KINDS,
  isPropertyKind,
  isPropertyScalarKind,
} from "./properties-value-control";

export type SchemaEditorHost = {
  root: HTMLElement;
  submit: (op: PropertySchemaOp, definition?: PropertySchemaDefinitionInput) => void;
  setStatus: (message: string) => void;
};

export type PropertySchemaEditor = {
  sync: (summary: PropertySummary) => void;
};

export const parseAllowedValues = (text: string): string[] => {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const line of text.split("\n")) {
    const value = line.trim();
    if (value === "" || seen.has(value)) {
      continue;
    }
    seen.add(value);
    out.push(value);
  }
  return out;
};

export const definitionError = (
  definition: PropertySchemaDefinitionInput
): string => {
  if (definition.type === "list" && !definition.items) {
    return "Choose the element kind a list holds.";
  }
  if (definition.type !== "list" && definition.items) {
    return "Only a list declares an element kind.";
  }
  return "";
};

const fillKindOptions = (
  select: HTMLSelectElement,
  kinds: readonly string[],
  blankLabel: string
): void => {
  select.textContent = "";
  if (blankLabel !== "") {
    const blank = select.ownerDocument.createElement("option");
    blank.value = "";
    blank.textContent = blankLabel;
    select.appendChild(blank);
  }
  for (const kind of kinds) {
    const option = select.ownerDocument.createElement("option");
    option.value = kind;
    option.textContent = kind;
    select.appendChild(option);
  }
};

// createPropertySchemaEditor drives the definition form. It is deliberately
// conservative: it never fills `required` or `allowed` from observed data, and
// adopting an inferred definition only seeds the form so the change is
// reviewed before it is previewed.
export const createPropertySchemaEditor = (
  host: SchemaEditorHost
): PropertySchemaEditor | null => {
  const section = queryElement(host.root, "[data-property-schema]", HTMLElement);
  if (!section) {
    return null;
  }
  const typeEl = queryElement(section, "[data-property-schema-type]", HTMLSelectElement);
  const itemsEl = queryElement(section, "[data-property-schema-items]", HTMLSelectElement);
  const requiredEl = queryElement(
    section,
    "[data-property-schema-required]",
    HTMLInputElement
  );
  const allowedEl = queryElement(
    section,
    "[data-property-schema-allowed]",
    HTMLTextAreaElement
  );
  const saveEl = queryElement(section, "[data-property-schema-save]", HTMLButtonElement);
  const deleteEl = queryElement(section, "[data-property-schema-delete]", HTMLButtonElement);
  const adoptEl = queryElement(section, "[data-property-schema-adopt]", HTMLButtonElement);
  const errorEl = queryElement(section, "[data-property-schema-error]", HTMLElement);
  if (!typeEl || !itemsEl || !requiredEl || !allowedEl || !saveEl) {
    return null;
  }

  fillKindOptions(typeEl, PROPERTY_KINDS, "");
  fillKindOptions(itemsEl, PROPERTY_SCALAR_KINDS, "");

  const itemsRow = itemsEl.closest<HTMLElement>("[data-property-schema-items-row]");
  let proposed: { type: string; items?: string } | null = null;

  const showError = (message: string): void => {
    if (errorEl) {
      errorEl.textContent = message;
      errorEl.hidden = message === "";
    }
  };

  const syncItemsVisibility = (): void => {
    const isList = typeEl.value === "list";
    if (itemsRow) {
      itemsRow.hidden = !isList;
    }
    itemsEl.disabled = !isList;
  };

  const readDefinition = (): PropertySchemaDefinitionInput | null => {
    if (!isPropertyKind(typeEl.value)) {
      showError("Choose a type.");
      return null;
    }
    const type: PropertyKind = typeEl.value;
    const items: PropertyScalarKind | undefined =
      type === "list" && isPropertyScalarKind(itemsEl.value) ? itemsEl.value : undefined;
    const definition: PropertySchemaDefinitionInput = {
      type,
      items,
      required: requiredEl.checked,
      allowed: parseAllowedValues(allowedEl.value),
    };
    const message = definitionError(definition);
    if (message !== "") {
      showError(message);
      return null;
    }
    showError("");
    return definition;
  };

  typeEl.addEventListener("change", syncItemsVisibility);
  saveEl.addEventListener("click", () => {
    const definition = readDefinition();
    if (definition) {
      host.submit("upsert", definition);
    }
  });
  deleteEl?.addEventListener("click", () => {
    host.submit("delete");
  });
  adoptEl?.addEventListener("click", () => {
    if (!proposed) {
      return;
    }
    typeEl.value = proposed.type;
    itemsEl.value = proposed.items || "";
    requiredEl.checked = false;
    allowedEl.value = "";
    syncItemsVisibility();
    showError("");
    host.setStatus("Inferred definition loaded. Review it, then save to preview the change.");
    typeEl.focus();
  });

  return {
    sync: (summary: PropertySummary): void => {
      const definition = summary.definition;
      typeEl.value = definition?.type || "string";
      itemsEl.value = definition?.items || "";
      requiredEl.checked = definition?.required || false;
      allowedEl.value = (definition?.allowed || []).join("\n");
      syncItemsVisibility();
      showError("");
      if (deleteEl) {
        deleteEl.disabled = !summary.configured;
      }
      proposed = summary.proposal
        ? { type: summary.proposal.type, items: summary.proposal.items }
        : null;
      if (adoptEl) {
        adoptEl.hidden = proposed === null;
      }
    },
  };
};
