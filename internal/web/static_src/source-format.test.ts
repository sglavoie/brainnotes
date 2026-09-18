import { describe, expect, it } from "vitest";

import { formatSource, lineRange, type SourceFormatCommand } from "./source-format";

// apply is what an editor surface would do with the edit, so a test can assert
// on the resulting document instead of on four offsets.
const apply = (
  doc: string,
  selection: { from: number; to: number },
  command: SourceFormatCommand,
  options?: { headingLevel?: number }
): { doc: string; selected: string } => {
  const edit = formatSource(doc, selection, command, options);
  const next = doc.slice(0, edit.from) + edit.insert + doc.slice(edit.to);
  return { doc: next, selected: next.slice(edit.selectionFrom, edit.selectionTo) };
};

const at = (doc: string, needle: string) => {
  const from = doc.indexOf(needle);
  return { from, to: from + needle.length };
};

describe("inline formatting", () => {
  it("wraps the selection and leaves the text selected, not the markers", () => {
    const result = apply("a word here", at("a word here", "word"), "bold");
    expect(result.doc).toBe("a **word** here");
    expect(result.selected).toBe("word");
  });

  it("inserts an empty pair at a caret and puts the caret between the markers", () => {
    const result = apply("ab", { from: 1, to: 1 }, "code");
    expect(result.doc).toBe("a``b");
    expect(result.selected).toBe("");
    expect(formatSource("ab", { from: 1, to: 1 }, "code").selectionFrom).toBe(2);
  });

  it("unwraps when the markers are inside the selection", () => {
    const doc = "a **word** here";
    const result = apply(doc, at(doc, "**word**"), "bold");
    expect(result.doc).toBe("a word here");
    expect(result.selected).toBe("word");
  });

  // This is the second press of Ctrl+B. The first left "word" selected with the
  // markers outside it, so a toggle that only looked inside the selection would
  // wrap again and produce ****word****.
  it("unwraps when the markers sit just outside the selection", () => {
    const doc = "a **word** here";
    const result = apply(doc, at(doc, "word"), "bold");
    expect(result.doc).toBe("a word here");
    expect(result.selected).toBe("word");
  });

  // Emphasis uses `_` precisely so this case is decidable. With `*` for italic,
  // `**bold**` starts and ends with the italic marker and the obvious toggle
  // silently downgrades it.
  it("italicising bold text nests rather than unwrapping it", () => {
    const doc = "**bold**";
    const result = apply(doc, at(doc, "**bold**"), "italic");
    expect(result.doc).toBe("_**bold**_");
  });
});

describe("heading", () => {
  it("adds the requested level to a plain line", () => {
    expect(apply("Title", { from: 2, to: 2 }, "heading", { headingLevel: 2 }).doc).toBe("## Title");
  });

  it("removes the level the line already carries", () => {
    expect(apply("## Title", { from: 4, to: 4 }, "heading", { headingLevel: 2 }).doc).toBe("Title");
  });

  it("replaces a different level rather than stacking hashes", () => {
    expect(apply("## Title", { from: 4, to: 4 }, "heading", { headingLevel: 4 }).doc).toBe("#### Title");
  });

  it("clamps the level into 1..6", () => {
    expect(apply("Title", { from: 0, to: 0 }, "heading", { headingLevel: 9 }).doc).toBe("###### Title");
    expect(apply("Title", { from: 0, to: 0 }, "heading", { headingLevel: 0 }).doc).toBe("# Title");
  });

  it("promotes every line a multi-line selection touches", () => {
    const doc = "one\ntwo\nthree";
    expect(apply(doc, { from: 1, to: 5 }, "heading").doc).toBe("# one\n# two\nthree");
  });
});

describe("bullet", () => {
  it("bullets the caret's line", () => {
    expect(apply("item", { from: 0, to: 0 }, "bullet").doc).toBe("- item");
  });

  it("removes the bullet when every touched line already has one", () => {
    const doc = "- one\n- two";
    expect(apply(doc, { from: 0, to: doc.length }, "bullet").doc).toBe("one\ntwo");
  });

  // Flipping each line independently would invert a half-marked list instead of
  // finishing it, which is never what somebody selecting a run of lines wants.
  it("completes a partly-bulleted run rather than inverting it", () => {
    const doc = "- one\ntwo";
    expect(apply(doc, { from: 0, to: doc.length }, "bullet").doc).toBe("- one\n- two");
  });

  it("leaves blank lines alone", () => {
    const doc = "one\n\ntwo";
    expect(apply(doc, { from: 0, to: doc.length }, "bullet").doc).toBe("- one\n\n- two");
  });

  it("preserves indentation when removing a bullet", () => {
    const doc = "  - nested";
    expect(apply(doc, { from: 0, to: doc.length }, "bullet").doc).toBe("  nested");
  });
});

describe("links", () => {
  it("links the selection and selects the destination", () => {
    const doc = "see Beta now";
    const result = apply(doc, at(doc, "Beta"), "link");
    expect(result.doc).toBe("see [Beta](url) now");
    expect(result.selected).toBe("url");
  });

  it("offers both placeholders at a caret and selects the text one", () => {
    const result = apply("", { from: 0, to: 0 }, "link");
    expect(result.doc).toBe("[text](url)");
    expect(result.selected).toBe("text");
  });

  it("wraps a wikilink around the selection", () => {
    const doc = "see Beta";
    const result = apply(doc, at(doc, "Beta"), "wikilink");
    expect(result.doc).toBe("see [[Beta]]");
    expect(result.selected).toBe("Beta");
  });

  it("opens an empty wikilink with the caret where completion starts", () => {
    const edit = formatSource("", { from: 0, to: 0 }, "wikilink");
    expect(edit.insert).toBe("[[]]");
    expect(edit.selectionFrom).toBe(2);
    expect(edit.selectionTo).toBe(2);
  });
});

describe("lineRange", () => {
  // A selection ending exactly at a line start must not pull that line in, or
  // selecting one whole line with its trailing newline would format two.
  it("does not include the line a selection merely ends at the start of", () => {
    expect(lineRange("one\ntwo\n", { from: 0, to: 4 })).toEqual({ from: 0, to: 3 });
  });

  it("spans every line a selection touches", () => {
    expect(lineRange("one\ntwo\nthree", { from: 1, to: 9 })).toEqual({ from: 0, to: 13 });
  });

  it("clamps offsets beyond the document", () => {
    expect(lineRange("one", { from: -5, to: 99 })).toEqual({ from: 0, to: 3 });
  });
});
