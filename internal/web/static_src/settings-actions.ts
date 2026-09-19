import type { SettingValue, SettingsResult } from "./api";
import {
  settingConfirmMessage,
  settingNoChangeMessage,
  settingPendingSave,
  settingSavedMessage,
  type SettingPendingSave,
} from "./settings-model";
import { applySetting, previewSetting, type FetchLike } from "./settings-requests";
import type { StatusKind } from "./status";

export type SettingsWriteHooks = {
  /** Reports progress, refusals and the final outcome. */
  onStatus?: (message: string, kind: StatusKind) => void;
  /** Reports the receipt a write is waiting on, or null once nothing is pending. */
  onPending?: (pending: SettingPendingSave | null) => void;
  /** Fires once the write landed, carrying the reloaded settings table. */
  onSaved?: (result: SettingsResult) => void;
};

export type SettingsWriter = {
  preview: (key: string, op: "set" | "unset", value: SettingValue) => Promise<void>;
  confirm: () => Promise<void>;
  cancel: () => void;
  pending: () => SettingPendingSave | null;
  busy: () => boolean;
};

export type SettingsWriterDeps = {
  fetch: FetchLike;
  token: string;
  hooks?: SettingsWriteHooks;
};

/**
 * Drives one portable-setting write through preview and apply.
 *
 * Every write here stops on its rendered diff, with no auto-chain. A saved view
 * skips the question when it is *creating* a block, because there is no existing
 * text to lose; a setting always replaces or removes a line in a file the vault
 * already loads, so there is no equivalent case and the patch is always worth
 * confirming.
 *
 * Exactly one receipt is held at a time. Receipts are one-use and expire, so a
 * second would guarantee that at most one of them still applied — and every
 * apply drops the one it redeemed whether it succeeded or was refused, which is
 * what keeps a retry from being aimed at a receipt the server already consumed.
 */
export const createSettingsWriter = (deps: SettingsWriterDeps): SettingsWriter => {
  const hooks = deps.hooks || {};
  let pending: SettingPendingSave | null = null;
  let busy = false;

  const setStatus = (message: string, kind: StatusKind = ""): void => {
    hooks.onStatus?.(message, kind);
  };

  const setPending = (next: SettingPendingSave | null): void => {
    pending = next;
    hooks.onPending?.(next);
  };

  const preview = async (
    key: string,
    op: "set" | "unset",
    value: SettingValue
  ): Promise<void> => {
    if (busy) {
      return;
    }
    busy = true;
    setPending(null);
    setStatus(`Previewing "${key}"...`, "is-loading");
    try {
      const outcome = await previewSetting(deps.fetch, deps.token, {
        token: deps.token,
        op,
        key,
        value,
      });
      if (!outcome.ok) {
        setStatus(outcome.message, "is-error");
        return;
      }
      // No receipt means the patch changed nothing: the file already says this.
      if (outcome.result.preview_id === "") {
        setStatus(settingNoChangeMessage(key), "");
        return;
      }
      // An unset has no requested value to compare the projection against: it
      // asks for whatever the default resolves to, which is by definition what
      // the vault will then hold.
      const issued = settingPendingSave(key, op, op === "set" ? value : null, outcome.result);
      setPending(issued);
      setStatus(settingConfirmMessage(issued), "is-warning");
    } finally {
      busy = false;
    }
  };

  const confirm = async (): Promise<void> => {
    const issued = pending;
    if (busy || !issued) {
      setStatus("Preview the change before saving it.", "is-error");
      return;
    }
    busy = true;
    setStatus(`Saving "${issued.key}"...`, "is-loading");
    try {
      const outcome = await applySetting(deps.fetch, deps.token, issued.previewID);
      setPending(null);
      if (!outcome.ok) {
        setStatus(outcome.message, "is-error");
        return;
      }
      hooks.onSaved?.(outcome.result);
      setStatus(settingSavedMessage(issued), "is-success");
    } finally {
      busy = false;
    }
  };

  const cancel = (): void => {
    if (pending === null) {
      return;
    }
    setPending(null);
    setStatus("Change cancelled.", "");
  };

  return { preview, confirm, cancel, pending: () => pending, busy: () => busy };
};
