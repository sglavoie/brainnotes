import { describe, expect, it } from "vitest";
import { formatSource, type SourceFormatCommand } from "./source-format";

const format = (doc: string, command: SourceFormatCommand) =>
  formatSource(doc, { from: 0, to: doc.length }, command);

describe("block formatting", () => {
  it("converts lists without stacking markers and preserves nested indentation", () => {
    const doc = "- first\n  - [x] nested\n3. last";
    expect(format(doc, "task").insert).toBe("- [ ] first\n  - [x] nested\n- [ ] last");
    expect(format(doc, "numbered").insert).toBe("1. first\n  1. nested\n2. last");
    expect(format("1. first\n  - [x] nested", "bullet").insert).toBe("- first\n  - nested");
  });

  it("removes a consistent task list including checked and unchecked items", () => {
    expect(format("- [ ] first\n\n  * [X] second", "task").insert).toBe("first\n\n  second");
  });

  it("removes only one quote level and leaves lists inside quotes intact", () => {
    expect(format("> first\n> > nested\n> - item", "quote").insert).toBe("first\n> nested\n- item");
    expect(format("first\n\nsecond", "quote").insert).toBe("> first\n\n> second");
  });

  it.each(["bullet", "numbered", "task", "quote"] as const)("starts %s on an empty line with a caret after the marker", (command) => {
    const result = format("", command);
    expect(result.insert.length).toBeGreaterThan(0);
    expect(result.selectionFrom).toBe(result.insert.length);
    expect(result.selectionTo).toBe(result.insert.length);
  });

  it("does not format the next line when a selection ends at its start", () => {
    const result = formatSource("first\nsecond", { from: 0, to: 6 }, "task");
    expect(result.insert).toBe("- [ ] first");
    expect(result.to).toBe(5);
  });

  it("starts a list on an empty first line without touching the following paragraph", () => {
    const result = formatSource("\nParagraph", { from: 0, to: 0 }, "numbered");
    expect(result).toEqual({ from: 0, to: 0, insert: "1. ", selectionFrom: 3, selectionTo: 3 });
  });
});
