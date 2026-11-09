// @vitest-environment jsdom
import { expect, it } from "vitest";
import { recentDateLabel } from "./library-recent";

it("uses calendar days around midnight, and leaves future or invalid timestamps exact", () => {
  const now = new Date(2026, 10, 8, 0, 5);
  expect(recentDateLabel(new Date(2026, 10, 7, 23, 58), now)).toBe("Yesterday");
  expect(recentDateLabel(new Date(2026, 10, 8, 0, 1), now)).toBe("Today");
  expect(recentDateLabel(new Date(2026, 10, 5), now)).toBe("3 days ago");
  expect(recentDateLabel(new Date(2026, 10, 9), now)).toBe("");
  expect(recentDateLabel(new Date("invalid"), now)).toBe("");
});
