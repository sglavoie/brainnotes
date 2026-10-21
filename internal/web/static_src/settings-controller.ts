import type { SettingSummary } from "./api";
import { queryElement } from "./dom";
import { createSettingsWriter, type SettingsWriter } from "./settings-actions";
import {
  settingBlockedReason,
  settingValueOf,
  type SettingPendingSave,
} from "./settings-model";
import { renderConfigDiff, renderSettingsList, type SettingRow } from "./settings-render";
import { fetchSettings, type FetchLike } from "./settings-requests";
import { setStatusText, type StatusKind } from "./status";
import { parseDisplayedSetting } from "./settings-units";
import { controlText, markSettingDraft, settingRowsModified, updateSettingRow } from "./settings-rows";
import { offerWriteOutcomeCheck } from "./write-outcome-check";

export type SettingsPanelDeps = {
  root: HTMLElement;
  fetch: FetchLike;
  token: string;
};

export type SettingsPanel = {
  load: () => Promise<void>;
  pending: () => SettingPendingSave | null;
  /** Whether a row holds an unsaved edit or an unconfirmed preview. */
  modified: () => boolean;
};

type SettingsElements = {
  list: HTMLElement;
  status: HTMLElement;
  filter: HTMLInputElement | null;
  filterSummary: HTMLElement | null;
  diff: HTMLElement | null;
  confirm: HTMLButtonElement | null;
  cancel: HTMLButtonElement | null;
  refresh: HTMLButtonElement | null;
};

const readElements = (root: HTMLElement): SettingsElements | null => {
  const list = queryElement(root, "[data-settings-list]", HTMLElement);
  const status = queryElement(root, "[data-settings-status]", HTMLElement);
  if (!list || !status) {
    return null;
  }
  return {
    list,
    status,
    filter: queryElement(root, "[data-settings-filter]", HTMLInputElement),
    filterSummary: queryElement(root, "[data-settings-filter-summary]", HTMLElement),
    diff: queryElement(root, "[data-settings-diff]", HTMLElement),
    confirm: queryElement(root, "[data-settings-confirm]", HTMLButtonElement),
    cancel: queryElement(root, "[data-settings-cancel]", HTMLButtonElement),
    refresh: queryElement(root, "[data-settings-refresh]", HTMLButtonElement),
  };
};

const summarize = (count: number): [string, StatusKind] =>
  count === 0
    ? ["No editable settings.", "is-empty"]
    : [
        `${count} portable settings. Machine-local settings stay in brainnotes.local.toml and are not editable here.`,
        "",
      ];

const matchesSetting = (row: SettingRow, needle: string, keptKeys: string[]): boolean => {
  if (needle === "" || row.element.dataset.dirty === "true" || keptKeys.includes(row.key)) return true;
  const group = row.element.closest<HTMLElement>(".settings-group");
  return [
    row.key,
    group?.querySelector("h3")?.textContent,
    row.element.querySelector(".settings-name")?.textContent,
    row.summary.summary,
  ].join(" ").toLocaleLowerCase().includes(needle);
};

const syncSettingsGroupVisibility = (root: HTMLElement): void => {
  for (const group of root.querySelectorAll<HTMLElement>(".settings-group")) {
    const count = group.querySelectorAll(".settings-row:not([hidden])").length;
    group.hidden = count === 0;
    const heading = group.querySelector("h3");
    const link = heading && root.querySelector<HTMLAnchorElement>(`.settings-group-nav a[href="#${heading.id}"]`);
    if (link) link.hidden = count === 0;
  }
};

const filterSettingsRows = (root: HTMLElement, rows: SettingRow[], term: string, keptKeys: string[]): number => {
  const needle = term.trim().toLocaleLowerCase();
  let visible = 0;
  for (const row of rows) {
    const show = matchesSetting(row, needle, keptKeys);
    row.element.hidden = !show;
    if (show) visible += 1;
  }
  syncSettingsGroupVisibility(root);
  return visible;
};

// Rows the filter would hide stay visible while they hold something the person
// is still looking at: an unsaved edit, a pending preview, or the confirmation
// of the save just made. The last of those lasts until the filter text changes.
const bindSettingsFilter = (
  root: HTMLElement, elements: SettingsElements,
  rows: () => SettingRow[], keptKeys: () => string[], onFilterInput: () => void
): (() => void) => {
  const apply = (): void => {
    if (!elements.filter) return;
    const current = rows();
    const count = filterSettingsRows(root, current, elements.filter.value, keptKeys());
    if (elements.filterSummary) {
      const filtering = elements.filter.value.trim() !== "";
      elements.filterSummary.hidden = !filtering;
      elements.filterSummary.textContent = filtering
        ? `${count} of ${current.length} settings shown. Unsaved edits stay visible.` : "";
    }
  };
  elements.filter?.addEventListener("input", () => { onFilterInput(); apply(); });
  root.addEventListener("input", (event) => {
    if (event.target instanceof HTMLElement && event.target.matches("[data-settings-control]")) apply();
  });
  return apply;
};

// Settings arrive after the browser's initial fragment navigation. Honor a
// direct link once they exist, without taking focus from someone already typing.
const focusLinkedSetting = (root: HTMLElement): void => {
  const doc = root.ownerDocument;
  if (doc.activeElement !== doc.body) return;
  const id = doc.defaultView?.location.hash.slice(1);
  if (!id) return;
  const target = doc.getElementById(id);
  if (!target || !root.contains(target)) return;
  focusSettingTarget(target);
};

const focusSettingTarget = (target: HTMLElement): void => {
  if (target instanceof HTMLHeadingElement) { target.focus(); return; }
  if (!(target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement)) return;
  const editor = target.closest("details");
  if (editor) editor.open = true;
  if (target.disabled) target.closest("[data-settings-row]")?.scrollIntoView({ block: "center" });
  else target.focus();
};

/**
 * Binds one rendered row to the writer.
 *
 * A shadowed key is bound to nothing: its controls are already disabled, and the
 * row explains why, so there is no handler that could produce a request the
 * server is certain to refuse.
 */
const bindRow = (
  row: SettingRow, writer: SettingsWriter,
  activate: (row: SettingRow) => void,
  refuse: (message: string) => void
): void => {
  const preview = async (op: "set" | "unset"): Promise<void> => {
    if (writer.busy() || settingBlockedReason(row.summary)) return;
    activate(row);
    const raw = controlText(row.control);
    const parsed = op === "unset"
      ? { ok: true as const, value: settingValueOf(row.summary.kind, {}) }
      : parseDisplayedSetting(row.summary, raw);
    if (!parsed.ok) { refuse(parsed.message); return; }
    await writer.preview(row.key, op, parsed.value);
    if (controlText(row.control) !== raw) writer.cancel();
  };
  row.save.addEventListener("click", () => void preview("set"));
  row.unset.addEventListener("click", () => void preview("unset"));
  row.control.addEventListener("input", () => {
    markSettingDraft(row);
    if (writer.pending()?.key === row.key) writer.cancel();
  });
};

// Keep a single preview receipt, but move its controls and feedback beside the
// setting it describes. Existing status/recovery semantics stay together.
const settingFeedback = (root: HTMLElement, elements: SettingsElements): HTMLElement => {
  const feedback = root.ownerDocument.createElement("div");
  feedback.className = "settings-feedback";
  elements.list.before(feedback);
  const recovery = root.querySelector(".write-check-link");
  const controls = root.querySelector("[data-settings-confirm-block]");
  feedback.appendChild(elements.status);
  if (recovery) feedback.appendChild(recovery);
  if (controls) feedback.appendChild(controls);
  else for (const node of [elements.diff, elements.confirm, elements.cancel]) {
    if (node) feedback.appendChild(node);
  }
  return feedback;
};

const focusSettingFeedback = (elements: SettingsElements, row: SettingRow | null, pending: boolean): void => {
  if (!row) return;
  const focused = row.element.ownerDocument.activeElement;
  if (pending && (focused === row.save || focused === row.unset)) elements.confirm?.focus();
  if (!pending && (focused === elements.confirm || focused === elements.cancel)) row.save.focus();
};

const statusSetter = (root: HTMLElement, elements: SettingsElements) =>
  (message: string, kind: StatusKind = "", uncertain = false): void => {
    setStatusText(elements.status, message, kind);
    elements.status.dataset.writeUnconfirmed = String(uncertain);
    // The status text can grow after focus moved to Confirm. Keep the focused
    // action reachable after that layout change without moving anyone else's focus.
    if (elements.confirm === root.ownerDocument.activeElement) {
      elements.confirm?.scrollIntoView?.({ block: "nearest" });
    }
  };

const pendingRenderer = (
  elements: SettingsElements, active: () => SettingRow | null, applyFilter: () => void
) => (pending: SettingPendingSave | null): void => {
  if (!pending) focusSettingFeedback(elements, active(), false);
  renderConfigDiff(elements.diff, pending ? pending.diff : []);
  if (elements.confirm) {
    elements.confirm.hidden = pending === null;
    elements.confirm.textContent = pending?.op === "unset" ? "Reset setting to default" : "Save setting";
  }
  if (elements.cancel) {
    elements.cancel.hidden = pending === null;
  }
  if (pending) focusSettingFeedback(elements, active(), true);
  applyFilter();
};

// listBusyMarker marks the list busy for a read and returns the function that
// ends it. Reads are counted apart from the panel's `reading`, which an edit
// also bumps to discard a read: the list stays busy until the latest answers.
const listBusyMarker = (list: HTMLElement) => {
  let loading = 0;
  return (): (() => void) => {
    const listing = ++loading;
    list.setAttribute("aria-busy", "true");
    return () => {
      if (listing === loading) list.setAttribute("aria-busy", "false");
    };
  };
};

/**
 * Mounts the portable-settings panel.
 *
 * The rows are rendered from `/api/settings` alone, so the page can never name a
 * key the API does not serve: the machine-local overlay's keys are absent here
 * because they are absent there, not because this file filters them out.
 */
export const createSettingsPanel = (deps: SettingsPanelDeps): SettingsPanel | null => {
  const elements = readElements(deps.root);
  if (!elements) {
    return null;
  }

  const feedback = settingFeedback(deps.root, elements);
  let rows: SettingRow[] = [];
  let active: SettingRow | null = null;
  let submitted = "";
  let reading = 0;
  let savedKey = "";
  const applyFilter = bindSettingsFilter(
    deps.root, elements, () => rows,
    () => [writer.pending()?.key ?? "", savedKey].filter(Boolean),
    () => { savedKey = ""; }
  );
  const activate = (row: SettingRow): void => {
    reading += 1;
    active = row;
    submitted = controlText(row.control);
    row.editor.appendChild(feedback);
  };
  const setStatus = statusSetter(deps.root, elements);
  const showPending = pendingRenderer(elements, () => active, () => applyFilter());

  const render = (settings: SettingSummary[], saved = false): void => {
    if (rows.length === 0) {
      rows = renderSettingsList(elements.list, settings);
      rows.forEach((row) => bindRow(row, writer, activate, (message) => {
        writer.cancel();
        setStatus(message, "is-error");
      }));
      applyFilter();
      return;
    }
    for (const row of rows) {
      const summary = settings.find((item) => item.key === row.key);
      if (summary) updateSettingRow(row, summary, saved && row === active ? submitted : undefined);
    }
    applyFilter();
  };

  const writer = createSettingsWriter({
    fetch: deps.fetch,
    token: deps.token,
    hooks: {
      onStatus: setStatus,
      onPending: showPending,
      onSaved: (result) => { reading += 1; savedKey = active?.key ?? ""; render(result.settings, true); },
      onOutcomeCheck: (operationID) => {
        offerWriteOutcomeCheck(elements.status, operationID, deps.fetch, "Setting change", () => void refreshConfirmed());
      },
    },
  });

  // A confirmed outcome is the save that went unanswered, so the rows take the
  // values it wrote as a direct save would, without replacing its confirmation.
  const refreshConfirmed = async (): Promise<void> => {
    const issue = ++reading;
    const outcome = await fetchSettings(deps.fetch);
    if (issue !== reading || !outcome.ok) return;
    savedKey = active?.key ?? "";
    render(outcome.result.settings, true);
  };

  let linkedSettingVisited = false;
  const markListBusy = listBusyMarker(elements.list);
  const load = async (): Promise<void> => {
    if (writer.busy()) return;
    writer.cancel();
    const issue = ++reading;
    showPending(null);
    setStatus("Loading settings…", "is-loading");
    const listed = markListBusy();
    const outcome = await fetchSettings(deps.fetch);
    listed();
    if (issue !== reading) return;
    if (!outcome.ok) {
      setStatus(outcome.message, "is-error");
      return;
    }
    render(outcome.result.settings);
    setStatus(...summarize(outcome.result.settings.length));
    if (!linkedSettingVisited) {
      linkedSettingVisited = true;
      focusLinkedSetting(deps.root);
    }
  };

  elements.confirm?.addEventListener("click", () => void writer.confirm());
  elements.cancel?.addEventListener("click", () => writer.cancel());
  elements.refresh?.addEventListener("click", () => void load());
  showPending(null);

  const modified = (): boolean => writer.pending() !== null || settingRowsModified(rows);
  return { load, pending: () => writer.pending(), modified };
};
