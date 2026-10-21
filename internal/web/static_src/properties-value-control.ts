import type { PropertyKind, PropertyScalarKind, PropertyTypedValue } from "./api";

export const PROPERTY_SCALAR_KINDS: readonly PropertyScalarKind[] = [
  "string",
  "wikilink",
  "date",
  "datetime",
  "int",
  "float",
  "bool",
] as const;

export const PROPERTY_KINDS: readonly PropertyKind[] = [
  ...PROPERTY_SCALAR_KINDS,
  "list",
] as const;

export const isPropertyScalarKind = (value: string): value is PropertyScalarKind =>
  (PROPERTY_SCALAR_KINDS as readonly string[]).includes(value);

export const isPropertyKind = (value: string): value is PropertyKind =>
  (PROPERTY_KINDS as readonly string[]).includes(value);

const DATE_PATTERN = /^\d{4}-\d{2}-\d{2}$/;
const RFC3339_PATTERN =
  /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$/;
const INT_PATTERN = /^-?\d+$/;

const pad = (value: number): string => String(value).padStart(2, "0");

// utcOffsetLabel renders the browser's offset for a moment as `+HH:MM`. A
// datetime control collects a wall-clock time with no zone, so the offset has
// to be attached before the value can be a legal RFC3339 timestamp.
export const utcOffsetLabel = (date: Date): string => {
  const minutes = -date.getTimezoneOffset();
  const sign = minutes < 0 ? "-" : "+";
  const absolute = Math.abs(minutes);
  return `${sign}${pad(Math.floor(absolute / 60))}:${pad(absolute % 60)}`;
};

// datetimeLocalToRFC3339 converts the `datetime-local` spelling into the
// RFC3339 timestamp the server accepts. It returns the input unchanged when it
// is not a wall-clock spelling, so an already-zoned value passes through.
export const datetimeLocalToRFC3339 = (local: string): string => {
  const trimmed = local.trim();
  if (trimmed === "" || RFC3339_PATTERN.test(trimmed)) {
    return trimmed;
  }
  const match = /^(\d{4}-\d{2}-\d{2})T(\d{2}:\d{2})(:\d{2})?$/.exec(trimmed);
  if (!match) {
    return trimmed;
  }
  const seconds = match[3] || ":00";
  const parsed = new Date(`${match[1]}T${match[2]}${seconds}`);
  if (Number.isNaN(parsed.getTime())) {
    return trimmed;
  }
  return `${match[1]}T${match[2]}${seconds}${utcOffsetLabel(parsed)}`;
};

// rfc3339ToDatetimeLocal renders a stored timestamp for a `datetime-local`
// control. An unparseable value is returned unchanged so nothing is silently
// rewritten on screen.
export const rfc3339ToDatetimeLocal = (text: string): string => {
  const trimmed = text.trim();
  if (trimmed === "") {
    return "";
  }
  const parsed = new Date(trimmed);
  if (Number.isNaN(parsed.getTime())) {
    return trimmed;
  }
  return [
    `${parsed.getFullYear()}-${pad(parsed.getMonth() + 1)}-${pad(parsed.getDate())}`,
    `T${pad(parsed.getHours())}:${pad(parsed.getMinutes())}:${pad(parsed.getSeconds())}`,
  ].join("");
};

// wikilinkTargetError rejects anything that is not a bare link target. The
// server refuses brackets, headings, and aliases too; checking here turns a
// round trip into an inline message.
export const wikilinkTargetError = (text: string): string => {
  const target = text.trim();
  if (target === "") {
    return "Enter a note title.";
  }
  if (target.includes("[") || target.includes("]")) {
    return "Enter the bare target without [[ ]].";
  }
  if (target.includes("#")) {
    return "A wikilink property cannot carry a heading.";
  }
  if (target.includes("|")) {
    return "A wikilink property cannot carry an alias.";
  }
  return "";
};

// scalarValueError reports why text cannot be stored as kind, or "" when it
// can. The server revalidates every write; this only spares a round trip.
const scalarValueErrors: Record<PropertyScalarKind, (value: string, text: string) => string> = {
  string: (value) => (value === "" ? "Enter a value." : ""),
  wikilink: (_value, text) => wikilinkTargetError(text),
  date: (value) => (DATE_PATTERN.test(value) ? "" : "Enter a date as YYYY-MM-DD."),
  datetime: (value) =>
    RFC3339_PATTERN.test(datetimeLocalToRFC3339(value)) ? "" : "Enter a date and time.",
  int: (value) => (INT_PATTERN.test(value) ? "" : "Enter a whole number."),
  float: (value) => (value !== "" && Number.isFinite(Number(value)) ? "" : "Enter a number."),
  bool: (value) => (value === "true" || value === "false" ? "" : "Choose true or false."),
};

export const scalarValueError = (kind: PropertyScalarKind, text: string): string =>
  scalarValueErrors[kind](text.trim(), text);

export type ScalarControl = {
  element: HTMLElement;
  read: () => string;
  error: () => string;
  focus: () => void;
};

export type ScalarControlOptions = {
  kind: PropertyScalarKind;
  allowed: string[];
  text: string;
  id: string;
  label: string;
};

const inputTypeFor = (kind: PropertyScalarKind): string => {
  switch (kind) {
    case "date":
      return "date";
    case "datetime":
      return "datetime-local";
    case "int":
    case "float":
      return "number";
    default:
      return "text";
  }
};

const seedText = (kind: PropertyScalarKind, text: string): string =>
  kind === "datetime" ? rfc3339ToDatetimeLocal(text) : text;

const readText = (kind: PropertyScalarKind, raw: string): string =>
  kind === "datetime" ? datetimeLocalToRFC3339(raw) : raw.trim();

const createSelect = (
  doc: Document,
  options: ScalarControlOptions,
  choices: string[]
): HTMLSelectElement => {
  const select = doc.createElement("select");
  select.className = "properties-control properties-control-select";
  select.id = options.id;
  select.setAttribute("aria-label", options.label);
  select.setAttribute("data-property-control", options.kind);
  for (const choice of choices) {
    const option = doc.createElement("option");
    option.value = choice;
    option.textContent = choice;
    select.appendChild(option);
  }
  const current = options.text.trim();
  select.value = choices.includes(current) ? current : choices[0] || "";
  return select;
};

// createScalarControl builds the one editor for a single value of kind. A
// configured allow-list collapses the control to a choice, so a strict
// allow-list cannot be violated by typing.
export const createScalarControl = (
  doc: Document,
  options: ScalarControlOptions
): ScalarControl => {
  if (options.kind === "bool") {
    const select = createSelect(doc, options, ["true", "false"]);
    return {
      element: select,
      read: () => select.value,
      error: () => scalarValueError("bool", select.value),
      focus: () => select.focus(),
    };
  }
  if (options.allowed.length > 0) {
    const select = createSelect(doc, options, options.allowed);
    return {
      element: select,
      read: () => select.value,
      error: () =>
        options.allowed.includes(select.value) ? "" : "Choose an allowed value.",
      focus: () => select.focus(),
    };
  }

  const input = doc.createElement("input");
  input.className = "properties-control properties-control-input";
  input.type = inputTypeFor(options.kind);
  input.id = options.id;
  input.autocomplete = "off";
  input.spellcheck = false;
  input.setAttribute("aria-label", options.label);
  input.setAttribute("data-property-control", options.kind);
  if (options.kind === "int") {
    input.step = "1";
  }
  if (options.kind === "float") {
    input.step = "any";
  }
  if (options.kind === "datetime") {
    input.step = "1";
  }
  if (options.kind === "wikilink") {
    input.placeholder = "Note title";
  }
  input.value = seedText(options.kind, options.text);
  return {
    element: input,
    read: () => readText(options.kind, input.value),
    error: () => scalarValueError(options.kind, input.value),
    focus: () => input.focus(),
  };
};

export type ValueControl = {
  element: HTMLElement;
  read: () => PropertyTypedValue;
  error: () => string;
  focus: () => void;
};

export type ValueControlOptions = {
  kind: PropertyKind;
  items: PropertyScalarKind;
  allowed: string[];
  value?: PropertyTypedValue;
  id: string;
  label: string;
};

const listSeedTexts = (options: ValueControlOptions): string[] => {
  const items = options.value?.items || [];
  if (items.length === 0) {
    return [""];
  }
  return items.map((item) => item.text);
};

// createListControl builds a repeating editor over the element kind. Lists do
// not nest, so every row is a scalar control.
const createListControl = (doc: Document, options: ValueControlOptions): ValueControl => {
  const wrapper = doc.createElement("div");
  wrapper.className = "properties-control-list";
  wrapper.setAttribute("data-property-control", "list");

  const list = doc.createElement("ul");
  list.className = "properties-control-list-items";
  wrapper.appendChild(list);

  const controls: ScalarControl[] = [];
  let sequence = 0;

  const renumber = (): void => {
    const rows = Array.from(list.children);
    rows.forEach((row, index) => {
      const remove = row.querySelector("[data-property-list-remove]");
      if (remove instanceof HTMLButtonElement) {
        remove.setAttribute("aria-label", `Remove item ${index + 1}`);
        remove.disabled = rows.length <= 1;
      }
    });
  };

  const addRow = (text: string, focusIt: boolean): void => {
    sequence += 1;
    const row = doc.createElement("li");
    row.className = "properties-control-list-item";

    const control = createScalarControl(doc, {
      kind: options.items,
      allowed: options.allowed,
      text,
      id: `${options.id}-item-${sequence}`,
      label: `${options.label} item`,
    });
    controls.push(control);
    row.appendChild(control.element);

    const remove = doc.createElement("button");
    remove.type = "button";
    remove.className = "properties-button properties-control-remove";
    remove.setAttribute("data-property-list-remove", "");
    remove.textContent = "Remove";
    remove.addEventListener("click", () => {
      const index = controls.indexOf(control);
      if (index >= 0) {
        controls.splice(index, 1);
      }
      row.remove();
      renumber();
    });
    row.appendChild(remove);

    list.appendChild(row);
    renumber();
    if (focusIt) {
      control.focus();
    }
  };

  for (const text of listSeedTexts(options)) {
    addRow(text, false);
  }

  const add = doc.createElement("button");
  add.type = "button";
  add.className = "properties-button properties-control-add";
  add.setAttribute("data-property-list-add", "");
  add.textContent = "Add item";
  add.addEventListener("click", () => {
    addRow("", true);
  });
  wrapper.appendChild(add);

  return {
    element: wrapper,
    read: () => ({
      kind: "list",
      text: "",
      items: controls.map((control) => ({
        kind: options.items,
        text: control.read(),
      })),
    }),
    error: () => {
      if (controls.length === 0) {
        return "Add at least one item.";
      }
      for (const control of controls) {
        const message = control.error();
        if (message !== "") {
          return message;
        }
      }
      return "";
    },
    focus: () => controls[0]?.focus(),
  };
};

// createValueControl is the single entry point every workbench surface uses to
// edit a property value, so a list and a scalar are driven by one contract.
export const createValueControl = (
  doc: Document,
  options: ValueControlOptions
): ValueControl => {
  if (options.kind === "list") {
    return createListControl(doc, options);
  }
  const scalar = createScalarControl(doc, {
    kind: options.kind,
    allowed: options.allowed,
    text: options.value?.text || "",
    id: options.id,
    label: options.label,
  });
  return {
    element: scalar.element,
    read: () => ({ kind: options.kind, text: scalar.read() }),
    error: () => scalar.error(),
    focus: () => scalar.focus(),
  };
};
