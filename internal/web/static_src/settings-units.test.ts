import { describe, expect, it } from "vitest";
import type { SettingSummary } from "./api";
import { settingValueOf } from "./settings-model";
import { bytesAsMiB, parseDisplayedSetting, settingControlText } from "./settings-units";

const size = (bytes: number): SettingSummary => ({
  key: "attachments.max_file_bytes", kind: "int", value: settingValueOf("int", { number: bytes }),
  summary: "Upload limit", assigned: true, layer: "portable", shadowed: false,
});

describe("upload size editing", () => {
  it.each([0, 1, 1048577, 26214400, Number.MAX_SAFE_INTEGER])("round trips %i bytes exactly", (bytes) => {
    const setting = size(bytes);
    const text = settingControlText(setting);
    expect(parseDisplayedSetting(setting, text)).toEqual({ ok: true, value: setting.value });
  });
  it("accepts fractional MiB and never silently rounds fractional bytes", () => {
    expect(bytesAsMiB(26214400)).toBe("25");
    expect(parseDisplayedSetting(size(0), "0.5")).toEqual({ ok: true, value: settingValueOf("int", { number: 524288 }) });
    expect(parseDisplayedSetting(size(0), "0.1")).toMatchObject({ ok: false, message: expect.stringContaining("No rounding") });
  });
  it.each(["", "no", "1e3", "999999999999999999999999999999", "1." + "0".repeat(70)])("refuses invalid or excessive input %s", (raw) => {
    expect(parseDisplayedSetting(size(0), raw).ok).toBe(false);
  });
});
