import type { SettingSummary } from "./api";
import { queryElement } from "./dom";
import { createSettingsWriter, type SettingsWriter } from "./settings-actions";
import {
  parseSettingInput,
  settingBlockedReason,
  settingValueOf,
  type SettingPendingSave,
} from "./settings-model";
import { renderConfigDiff, renderSettingsList, type SettingRow } from "./settings-render";
import { fetchSettings, type FetchLike } from "./settings-requests";
import { setStatusText, type StatusKind } from "./status";
import { controlText, markSettingDraft, updateSettingRow } from "./settings-rows";

export type SettingsPanelDeps = {
  root: HTMLElement;
  fetch: FetchLike;
  token: string;
};

export type SettingsPanel = {
  load: () => Promise<void>;
  pending: () => SettingPendingSave | null;
};

type SettingsElements = {
  list: HTMLElement;
  status: HTMLElement;
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

// Settings arrive after the browser's initial fragment navigation. Honor a
// direct link once they exist, without taking focus from someone already typing.
const focusLinkedSetting = (root: HTMLElement): void => {
  const doc = root.ownerDocument;
  if (doc.activeElement !== doc.body) return;
  const id = doc.defaultView?.location.hash.slice(1);
  if (!id) return;
  const target = doc.getElementById(id);
  if (!(target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement) || !root.contains(target)) return;
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
      : parseSettingInput(row.summary.kind, raw);
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
  const activate = (row: SettingRow): void => {
    reading += 1;
    active = row;
    submitted = controlText(row.control);
    row.editor.appendChild(feedback);
  };
  const setStatus = (message: string, kind: StatusKind = "", uncertain = false): void => {
    setStatusText(elements.status, message, kind);
    elements.status.dataset.writeUnconfirmed = String(uncertain);
    // The status text can grow after focus moved to Confirm. Keep the focused
    // action reachable after that layout change without moving anyone else's focus.
    if (elements.confirm === deps.root.ownerDocument.activeElement) {
      elements.confirm?.scrollIntoView?.({ block: "nearest" });
    }
  };

  const showPending = (pending: SettingPendingSave | null): void => {
    if (!pending) focusSettingFeedback(elements, active, false);
    renderConfigDiff(elements.diff, pending ? pending.diff : []);
    if (elements.confirm) {
      elements.confirm.hidden = pending === null;
      elements.confirm.textContent = pending?.op === "unset" ? "Reset setting to default" : "Save setting";
    }
    if (elements.cancel) {
      elements.cancel.hidden = pending === null;
    }
    if (pending) focusSettingFeedback(elements, active, true);
  };

  const render = (settings: SettingSummary[], saved = false): void => {
    if (rows.length === 0) {
      rows = renderSettingsList(elements.list, settings);
      rows.forEach((row) => bindRow(row, writer, activate, (message) => {
        writer.cancel();
        setStatus(message, "is-error");
      }));
      return;
    }
    for (const row of rows) {
      const summary = settings.find((item) => item.key === row.key);
      if (summary) updateSettingRow(row, summary, saved && row === active ? submitted : undefined);
    }
  };

  const writer = createSettingsWriter({
    fetch: deps.fetch,
    token: deps.token,
    hooks: {
      onStatus: setStatus,
      onPending: showPending,
      onSaved: (result) => { reading += 1; render(result.settings, true); },
    },
  });

  let linkedSettingVisited = false;
  const load = async (): Promise<void> => {
    if (writer.busy()) return;
    writer.cancel();
    const issue = ++reading;
    showPending(null);
    setStatus("Loading settings...", "is-loading");
    const outcome = await fetchSettings(deps.fetch);
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

  return { load, pending: () => writer.pending() };
};
