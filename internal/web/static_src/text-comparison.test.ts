// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import { compareText, renderTextComparison } from "./text-comparison";

describe("text comparisons", () => {
  it.each([
    ["", "new\n"], ["removed\n", ""], ["same\nold\nend\n", "same\nnew\nend\n"],
    ["café\r\n", "café\n"], ["no newline", "no newline\n"],
    ["a\nb\na\n", "b\na\nb\n"], ["x\n".repeat(1200), "y\n".repeat(1200)],
  ])("preserves both versions exactly", (before, after) => {
    const result = compareText(before, after);
    expect(result.filter((line) => line.kind !== "added").map((line) => line.text).join("")).toBe(before);
    expect(result.filter((line) => line.kind !== "removed").map((line) => line.text).join("")).toBe(after);
  });
  it("collapses unchanged context and renders note markup as text", () => {
    const host = document.createElement("div");
    const context = "same\n".repeat(20);
    renderTextComparison(host, context + "old\n", context + "<img src=x onerror=alert(1)>\n", "Current note", "Your draft");
    expect(host.querySelector("details")?.open).toBe(false);
    expect(host.querySelector("summary")?.textContent).toBe("14 unchanged lines");
    expect(host.querySelector("img")).toBeNull();
    expect(host.querySelector(".comparison-added")?.textContent).toContain("<img");
    expect(host.textContent).toContain("Current note · + Your draft");
  });
  it("keeps an entirely rewritten long note bounded in DOM size", () => {
    const host = document.createElement("div");
    renderTextComparison(host, "old\n".repeat(20000), "new\n".repeat(20000), "Current note", "Your draft");
    expect(host.querySelectorAll(".comparison-line")).toHaveLength(2);
    expect(host.querySelector(".comparison-added code")?.textContent?.split("\n")).toHaveLength(20000);
  });
});
