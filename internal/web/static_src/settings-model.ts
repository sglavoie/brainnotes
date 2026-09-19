import type { PropertyConfigDiffLine, SettingKind, SettingSummary, SettingValue } from "./api";

// settingValueOf builds the transport form of one value. The kind is always
// carried, so the server can refuse a value that disagrees with the key instead
// of guessing which field was meant.
export const settingValueOf = (kind: SettingKind, partial: Partial<SettingValue>): SettingValue => ({
  kind,
  text: partial.text || "",
  number: partial.number || 0,
  flag: partial.flag || false,
  items: partial.items ? [...partial.items] : [],
});

/**
 * Renders one setting value the way a person reads it.
 *
 * An unassigned setting is rendered as the default it resolves to rather than
 * as blank, because "the file does not say" and "the file says the empty
 * string" are different states and only the row's `assigned` flag distinguishes
 * them — the value itself is the same either way.
 */
export const formatSettingValue = (value: SettingValue): string => {
  switch (value.kind) {
    case "bool":
      return value.flag ? "true" : "false";
    case "int":
      return String(value.number);
    case "string_list":
      return value.items.length === 0 ? "(empty list)" : value.items.join(", ");
    default:
      return value.text === "" ? "(empty)" : value.text;
  }
};

/** Renders one value into the text a control is seeded with. */
export const settingEditorText = (value: SettingValue): string => {
  switch (value.kind) {
    case "int":
      return String(value.number);
    case "string_list":
      return value.items.join("\n");
    case "bool":
      return value.flag ? "true" : "false";
    default:
      return value.text;
  }
};

export type SettingParse =
  | { ok: true; value: SettingValue }
  | { ok: false; message: string };

/**
 * Turns what a control holds into a typed value, or into the refusal that
 * explains why it is not one.
 *
 * This is the client's only type judgement, and it is deliberately narrow: it
 * decides whether the text *is* a value of the declared kind, never whether the
 * loader will accept it. `capture.heading = "Captures"` parses perfectly here
 * and is refused by the server, which re-decodes the whole configuration before
 * issuing a receipt — so the browser never has to hold a second copy of the
 * configuration's validation rules.
 */
export const parseSettingInput = (kind: SettingKind, raw: string): SettingParse => {
  switch (kind) {
    case "bool":
      return { ok: true, value: settingValueOf(kind, { flag: raw === "true" }) };
    case "int": {
      const text = raw.trim();
      if (text === "" || !/^-?\d+$/.test(text)) {
        return { ok: false, message: "This setting takes a whole number." };
      }
      const parsed = Number(text);
      if (!Number.isSafeInteger(parsed)) {
        return { ok: false, message: "That number is too large to store." };
      }
      return { ok: true, value: settingValueOf(kind, { number: parsed }) };
    }
    case "string_list":
      return {
        ok: true,
        value: settingValueOf(kind, {
          items: raw
            .split("\n")
            .map((item) => item.trim())
            .filter((item) => item !== ""),
        }),
      };
    default:
      return { ok: true, value: settingValueOf(kind, { text: raw.trim() }) };
  }
};

/**
 * Explains why a setting cannot be edited here, or "" when it can.
 *
 * A shadowed key is the one refusal worth stating in advance. The server refuses
 * it too, but by then the person has already typed a value: the portable file is
 * not what decides this vault's behaviour while the machine-local overlay
 * assigns the same key, so writing it would look like a change and be none.
 */
export const settingBlockedReason = (summary: SettingSummary): string =>
  summary.shadowed
    ? `"${summary.key}" is assigned in brainnotes.local.toml, which overrides the portable file. Remove it there before editing it here.`
    : "";

export const settingOriginLabel = (summary: SettingSummary): string => {
  if (!summary.assigned) {
    return "default";
  }
  return summary.layer === "local" ? "brainnotes.local.toml" : "brainnotes.toml";
};

// SettingPendingSave is one issued, not-yet-redeemed receipt. It is one-use, so
// holding more than one at a time would guarantee that at most one of them still
// applies.
//
// `effective` is how this vault would hold the value once written, and it is
// empty whenever that is exactly what was typed.
export type SettingPendingSave = {
  key: string;
  op: "set" | "unset";
  previewID: string;
  diff: PropertyConfigDiffLine[];
  effective: string;
};

export const settingPendingSave = (
  key: string,
  op: "set" | "unset",
  requested: SettingValue | null,
  result: { preview_id: string; config_diff: PropertyConfigDiffLine[]; settings: SettingSummary[] }
): SettingPendingSave => ({
  key,
  op,
  previewID: result.preview_id,
  diff: [...result.config_diff],
  effective: projectedSetting(key, requested, result.settings),
});

/**
 * Names how the vault would hold the value, or "" when it would hold what was
 * typed.
 *
 * The configuration loader normalizes as well as validates, and several of its
 * rules are silent: an absolute `templates.dir` falls back to the default, a
 * duration is stored in its canonical spelling, a list is merged with the
 * entries the vault always ignores. The patch is legal and the diff is exact in
 * every one of those cases, and the value the vault ends up using is still not
 * the one that was typed — so the difference is named on the confirmation,
 * before the write, rather than left to be noticed in the row afterwards.
 *
 * The projection is the server's: a preview re-decodes the patched pair and
 * answers the settings table it would leave behind, which is what is read here.
 */
const projectedSetting = (
  key: string,
  requested: SettingValue | null,
  settings: SettingSummary[]
): string => {
  const stored = settings.find((summary) => summary.key === key);
  if (!requested || !stored) {
    return "";
  }
  const shown = formatSettingValue(stored.value);
  return shown === formatSettingValue(requested) ? "" : shown;
};

export const settingConfirmMessage = (pending: SettingPendingSave): string => {
  if (pending.op === "unset") {
    return `Remove "${pending.key}" from brainnotes.toml? Review the change, then confirm.`;
  }
  const stored = pending.effective === "" ? "" : ` This vault will store it as ${pending.effective}.`;
  return `Write "${pending.key}" to brainnotes.toml? Review the change, then confirm.${stored}`;
};

export const settingSavedMessage = (pending: SettingPendingSave): string =>
  pending.op === "unset"
    ? `Removed "${pending.key}". It is back to its default.`
    : `Saved "${pending.key}".`;

export const settingNoChangeMessage = (key: string): string =>
  `"${key}" already holds that value.`;
