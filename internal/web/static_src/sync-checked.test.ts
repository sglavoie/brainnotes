import { expect, it } from "vitest";
import { syncCheckedText } from "./sync-checked";

it("dates the server observation rather than the fetch, including clock skew", () => {
  const now = Date.parse("2026-10-18T12:00:00Z");
  expect(syncCheckedText("2026-10-18T11:59:50Z", now)).toBe("Checked just now.");
  expect(syncCheckedText("2026-10-18T11:58:00Z", now)).toBe("Checked 2 minutes ago.");
  expect(syncCheckedText("2026-10-18T11:00:00Z", now)).toBe("Checked 1 hour ago.");
  expect(syncCheckedText("2026-10-18T12:02:00Z", now)).not.toContain("ago");
  expect(syncCheckedText("", now)).toBe("");
  expect(syncCheckedText("invalid", now)).toBe("Checked invalid.");
});
