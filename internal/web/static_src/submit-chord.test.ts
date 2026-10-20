import { describe, expect, it } from "vitest";

import { isSubmitChord } from "./submit-chord";

const key = (init: KeyboardEventInit): KeyboardEvent => ({
  key: "Enter", ctrlKey: false, metaKey: false, isComposing: false, ...init,
}) as KeyboardEvent;

describe("isSubmitChord", () => {
  it("accepts Ctrl or Cmd with Enter", () => {
    expect(isSubmitChord(key({ ctrlKey: true }))).toBe(true);
    expect(isSubmitChord(key({ metaKey: true }))).toBe(true);
  });

  it("ignores plain Enter, other keys and Enter that commits an IME composition", () => {
    expect(isSubmitChord(key({}))).toBe(false);
    expect(isSubmitChord(key({ key: "a", ctrlKey: true }))).toBe(false);
    expect(isSubmitChord(key({ ctrlKey: true, isComposing: true }))).toBe(false);
  });
});
