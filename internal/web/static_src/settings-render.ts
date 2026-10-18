import type { PropertyConfigDiffLine, SettingSummary, TemplateSummary } from "./api";
import {
  settingBlockedReason,
  settingOriginLabel,
} from "./settings-model";

import { settingControlText, usesMiB } from "./settings-units";
import { settingDisplayValue, settingGroup, settingName, settingUnit } from "./settings-presentation";

/** Renders a configuration patch as the unified text a person reads. */
export const configDiffText = (diff: PropertyConfigDiffLine[]): string =>
  diff.map((line) => `${line.kind === "-" ? "-" : "+"} ${line.text}`).join("\n");

/**
 * Fills a diff block, or hides it when there is nothing pending.
 *
 * Both write classes on this page confirm before they write, so the block is
 * always a question — unlike the saved-views block, which doubles as a receipt
 * for the one write that auto-chains. Nothing here chains.
 */
export const renderConfigDiff = (
  block: HTMLElement | null,
  diff: PropertyConfigDiffLine[]
): void => {
  if (!block) {
    return;
  }
  if (diff.length === 0) {
    block.textContent = "";
    block.hidden = true;
    return;
  }
  block.textContent = configDiffText(diff);
  block.hidden = false;
};

const appendLabelled = (
  row: HTMLElement,
  className: string,
  text: string
): HTMLElement => {
  const element = row.ownerDocument.createElement("span");
  element.className = className;
  element.textContent = text;
  row.appendChild(element);
  return element;
};

/**
 * Builds the control one setting is edited through.
 *
 * The control follows the declared kind rather than being one text box for
 * everything, which is what "typed editing" means here: a bool is a checkbox
 * that cannot hold a typo, an int is a number field, and a list is a textarea
 * where one line is one item. A string is the only free-text case, and it is the
 * only one where the server's own validation is the first thing that can refuse
 * the input.
 */
export const buildSettingControl = (
  doc: Document,
  summary: SettingSummary
): HTMLInputElement | HTMLTextAreaElement => {
  if (summary.kind === "bool") {
    const box = doc.createElement("input");
    box.type = "checkbox";
    box.className = "settings-control settings-control-check";
    box.checked = summary.value.flag;
    return box;
  }
  if (summary.kind === "string_list") {
    const area = doc.createElement("textarea");
    area.className = "settings-control settings-control-list";
    area.rows = 3;
    area.spellcheck = false;
    area.value = settingControlText(summary);
    return area;
  }
  const field = doc.createElement("input");
  field.type = summary.kind === "int" ? "number" : "text";
  field.className = "settings-control settings-control-text";
  if (usesMiB(summary)) field.step = "any";
  field.spellcheck = false;
  field.value = settingControlText(summary);
  return field;
};

export type SettingRow = {
  key: string;
  element: HTMLLIElement;
  control: HTMLInputElement | HTMLTextAreaElement;
  save: HTMLButtonElement;
  unset: HTMLButtonElement;
  editor: HTMLDetailsElement;
  summary: SettingSummary;
};

const buildButton = (
  doc: Document,
  label: string,
  className: string,
  attribute: string
): HTMLButtonElement => {
  const button = doc.createElement("button");
  button.type = "button";
  button.className = className;
  button.textContent = label;
  button.setAttribute(attribute, "");
  return button;
};

/**
 * Renders one editable setting.
 *
 * The row says three separate things, because collapsing them would hide the
 * one state this surface exists to make visible: what the setting resolves to,
 * whether a file assigned it or it is the built-in default, and — for a portable
 * key the machine-local overlay also assigns — that editing the portable file
 * here would change nothing until the overlay stops naming it. That last case
 * disables the controls and says why, rather than letting a person type a value
 * the server is going to refuse.
 */
export const renderSettingRow = (doc: Document, summary: SettingSummary): SettingRow => {
  const item = doc.createElement("li");
  item.className = "settings-row";
  item.setAttribute("data-settings-row", summary.key);

  const header = doc.createElement("div");
  header.className = "settings-row-header";
  appendLabelled(item, "settings-name", settingName(summary.key));
  const info = doc.createElement("details");
  info.className = "settings-info";
  const infoLabel = doc.createElement("summary");
  infoLabel.textContent = "Configuration details";
  info.appendChild(infoLabel);
  appendLabelled(header, "settings-key", summary.key);
  appendLabelled(header, "settings-kind", summary.kind);
  const origin = appendLabelled(header, "settings-origin", settingOriginLabel(summary));
  origin.setAttribute("data-settings-origin", summary.assigned ? summary.layer : "default");
  info.appendChild(header);

  appendLabelled(item, "settings-summary", usesMiB(summary) ? summary.summary.replace(/\bin bytes\b/g, "in MiB") : summary.summary);
  const current = appendLabelled(item, "settings-current", settingDisplayValue(summary));
  current.setAttribute("data-settings-current", "");

  const editor = doc.createElement("details");
  editor.className = "settings-editor";
  const edit = doc.createElement("summary");
  edit.textContent = "Edit";
  edit.setAttribute("data-settings-edit", "");
  edit.setAttribute("aria-label", `Edit ${settingName(summary.key)}`);
  editor.appendChild(edit);
  item.appendChild(editor);
  const control = buildSettingControl(doc, summary);
  control.setAttribute("data-settings-control", "");
  control.id = `setting-${summary.key.replace(/\./g, "-")}`;
  const label = doc.createElement("label");
  label.className = "settings-control-label";
  label.htmlFor = control.id;
  const unit = settingUnit(summary.key);
  label.textContent = unit ? `Value (${unit})` : "Value";
  control.setAttribute("aria-label", `${settingName(summary.key)}${unit ? ` (${unit})` : ""}`);
  editor.appendChild(label);
  editor.appendChild(control);

  const actions = doc.createElement("div");
  actions.className = "settings-row-actions";
  const save = buildButton(doc, "Save", "settings-button settings-button-primary", "data-settings-save");
  const unset = buildButton(doc, "Reset to default", "settings-button", "data-settings-unset");
  unset.disabled = !summary.assigned;
  actions.appendChild(save);
  actions.appendChild(unset);
  editor.appendChild(actions);
  editor.appendChild(info);

  const marker = appendLabelled(item, "settings-dirty", "Unsaved changes");
  marker.setAttribute("data-settings-dirty", "");
  marker.hidden = true;
  const blocked = settingBlockedReason(summary);
  const notice = appendLabelled(item, "settings-shadowed", blocked);
  notice.setAttribute("data-settings-shadowed", "");
  notice.setAttribute("role", "note");
  notice.hidden = blocked === "";
  if (blocked !== "") {
    item.classList.add("is-shadowed");
    control.disabled = true;
    save.disabled = true;
    unset.disabled = true;
  }

  return { key: summary.key, element: item, control, save, unset, editor, summary };
};

export const renderSettingsList = (
  list: HTMLElement,
  settings: SettingSummary[]
): SettingRow[] => {
  const doc = list.ownerDocument;
  list.textContent = "";
  const groups = new Map<string, HTMLUListElement>();
  const nav = doc.createElement("nav");
  nav.className = "settings-group-nav";
  nav.setAttribute("aria-label", "Settings groups");
  list.parentElement?.querySelector(".settings-group-nav")?.remove();
  list.before(nav);
  return settings.map((summary) => {
    const group = settingGroup(summary.key);
    let section = groups.get(group);
    if (!section) {
      const item = doc.createElement("li");
      item.className = "settings-group";
      const heading = doc.createElement("h3");
      heading.textContent = group;
      heading.id = `settings-group-${summary.key.split(".")[0]}`;
      heading.tabIndex = -1;
      const link = doc.createElement("a");
      link.href = `#${heading.id}`;
      link.textContent = group;
      nav.appendChild(link);
      section = doc.createElement("ul");
      section.className = "settings-group-list";
      item.append(heading, section);
      list.appendChild(item);
      groups.set(group, section);
    }
    const row = renderSettingRow(doc, summary);
    section.appendChild(row.element);
    return row;
  });
};

/**
 * Renders the template picker.
 *
 * Every entry is a button rather than a link: a template has no page of its own
 * and is never addressable by path, which is the same reason the read endpoint
 * takes a name in a query parameter instead of a path segment.
 */
export const renderTemplateList = (
  list: HTMLElement,
  templates: TemplateSummary[],
  selected: string
): void => {
  const doc = list.ownerDocument;
  list.textContent = "";
  for (const summary of templates) {
    const item = doc.createElement("li");
    item.className = "templates-row";
    const open = doc.createElement("button");
    open.type = "button";
    open.className = "templates-open";
    open.textContent = summary.name;
    open.setAttribute("data-templates-open", summary.name);
    if (summary.name === selected) {
      open.classList.add("is-selected");
      open.setAttribute("aria-current", "true");
    }
    item.appendChild(open);
    const size = doc.createElement("span");
    size.className = "templates-size";
    size.textContent = `${summary.size} bytes`;
    item.appendChild(size);
    list.appendChild(item);
  }
};
