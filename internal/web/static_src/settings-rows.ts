import type { SettingSummary } from "./api";
import { settingBlockedReason, settingEditorText, settingOriginLabel } from "./settings-model";
import { settingDisplayValue } from "./settings-presentation";
import type { SettingRow } from "./settings-render";

export const controlText = (control: HTMLInputElement | HTMLTextAreaElement): string =>
  control instanceof HTMLInputElement && control.type === "checkbox" ? String(control.checked) : control.value;

const setControl = (row: SettingRow, summary: SettingSummary): void => {
  if (row.control instanceof HTMLInputElement && row.control.type === "checkbox") {
    row.control.checked = summary.value.flag;
  } else {
    row.control.value = settingEditorText(summary.value);
  }
};

export const markSettingDraft = (row: SettingRow): void => {
  const dirty = controlText(row.control) !== settingEditorText(row.summary.value);
  row.element.dataset.dirty = String(dirty);
  const marker = row.element.querySelector<HTMLElement>("[data-settings-dirty]");
  if (marker) marker.hidden = !dirty;
};

// Reconcile in place: controls, selection, disclosures and focus belong to the
// person editing. A read may update the saved value, but must not erase a draft.
export const updateSettingRow = (row: SettingRow, summary: SettingSummary, savedText?: string): void => {
  const current = controlText(row.control);
  const keepDraft = savedText === undefined
    ? current !== settingEditorText(row.summary.value)
    : current !== savedText;
  if (!keepDraft) setControl(row, summary);
  row.summary = summary;
  row.element.querySelector<HTMLElement>("[data-settings-current]")!.textContent = settingDisplayValue(summary);
  const origin = row.element.querySelector<HTMLElement>("[data-settings-origin]")!;
  origin.textContent = settingOriginLabel(summary);
  origin.dataset.settingsOrigin = summary.assigned ? summary.layer : "default";
  const blocked = settingBlockedReason(summary);
  row.control.disabled = row.save.disabled = blocked !== "";
  row.unset.disabled = blocked !== "" || !summary.assigned;
  row.element.classList.toggle("is-shadowed", blocked !== "");
  const notice = row.element.querySelector<HTMLElement>("[data-settings-shadowed]")!;
  notice.textContent = blocked;
  notice.hidden = blocked === "";
  markSettingDraft(row);
};
