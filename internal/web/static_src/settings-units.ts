import type { SettingSummary } from "./api";
import { parseSettingInput, settingEditorText, settingValueOf, type SettingParse } from "./settings-model";

export const usesMiB = (summary: SettingSummary): boolean =>
  summary.kind === "int" && summary.key.endsWith("_bytes");

// A byte is exactly 1 / 2^20 MiB. Keep every digit so merely opening and
// saving a setting cannot round a pre-existing byte count.
export const bytesAsMiB = (bytes: number): string => {
  const value = BigInt(bytes);
  const magnitude = value < 0n ? -value : value;
  const fraction = ((magnitude % 1048576n) * 5n ** 20n).toString().padStart(20, "0").replace(/0+$/, "");
  return `${value < 0n ? "-" : ""}${magnitude / 1048576n}${fraction ? `.${fraction}` : ""}`;
};

export const settingControlText = (summary: SettingSummary): string =>
  usesMiB(summary) ? bytesAsMiB(summary.value.number) : settingEditorText(summary.value);

export const parseDisplayedSetting = (summary: SettingSummary, raw: string): SettingParse => {
  if (!usesMiB(summary)) return parseSettingInput(summary.kind, raw);
  const text = raw.trim();
  if (!/^-?\d+(\.\d+)?$/.test(text) || text.length > 64) {
    return { ok: false, message: "Enter a size in MiB, for example 25 or 0.5." };
  }
  const decimals = text.split(".")[1]?.length || 0;
  const numerator = BigInt(text.replace(".", "")) * 1048576n;
  const denominator = 10n ** BigInt(decimals);
  if (numerator % denominator !== 0n) {
    return { ok: false, message: "This size must resolve to a whole number of bytes. No rounding was applied." };
  }
  const bytes = Number(numerator / denominator);
  if (!Number.isSafeInteger(bytes)) return { ok: false, message: "That size is too large to store." };
  return { ok: true, value: settingValueOf("int", { number: bytes }) };
};
