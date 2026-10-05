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

/** Reads what a control holds as the text the model parses. */
const controlText = (control: HTMLInputElement | HTMLTextAreaElement): string => {
  if (control instanceof HTMLInputElement && control.type === "checkbox") {
    return control.checked ? "true" : "false";
  }
  return control.value;
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
  row: SettingRow,
  summary: SettingSummary,
  writer: SettingsWriter,
  refuse: (message: string) => void
): void => {
  if (settingBlockedReason(summary) !== "") {
    return;
  }
  row.save.addEventListener("click", () => {
    const parsed = parseSettingInput(summary.kind, controlText(row.control));
    if (!parsed.ok) {
      refuse(parsed.message);
      return;
    }
    void writer.preview(summary.key, "set", parsed.value);
  });
  row.unset.addEventListener("click", () => {
    void writer.preview(summary.key, "unset", settingValueOf(summary.kind, {}));
  });
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

  const setStatus = (message: string, kind: StatusKind = ""): void => {
    setStatusText(elements.status, message, kind);
  };

  const showPending = (pending: SettingPendingSave | null): void => {
    renderConfigDiff(elements.diff, pending ? pending.diff : []);
    if (elements.confirm) {
      elements.confirm.hidden = pending === null;
    }
    if (elements.cancel) {
      elements.cancel.hidden = pending === null;
    }
  };

  const render = (settings: SettingSummary[]): void => {
    const rows = renderSettingsList(elements.list, settings);
    rows.forEach((row, index) => {
      bindRow(row, settings[index], writer, (message) => {
        showPending(null);
        setStatus(message, "is-error");
      });
    });
  };

  const writer = createSettingsWriter({
    fetch: deps.fetch,
    token: deps.token,
    hooks: {
      onStatus: setStatus,
      onPending: showPending,
      onSaved: (result) => render(result.settings),
    },
  });

  let linkedSettingVisited = false;
  const load = async (): Promise<void> => {
    showPending(null);
    setStatus("Loading settings...", "is-loading");
    const outcome = await fetchSettings(deps.fetch);
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
