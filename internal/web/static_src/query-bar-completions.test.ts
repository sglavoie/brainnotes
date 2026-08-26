import { describe, expect, it } from "vitest";

import type { QueryCompletion } from "./api";
import {
  applyCompletion,
  completionContextAt,
  completionLabel,
  filterCompletions,
  mergeCompletions,
  tokenAtCursor,
} from "./query-bar-completions";

const completion = (overrides: Partial<QueryCompletion> = {}): QueryCompletion => ({
  value: "focus",
  insert: "focus",
  kind: "tag",
  detail: "",
  count: 0,
  ...overrides,
});

describe("token at cursor", () => {
  it("takes the token the caret sits in, not the whole line", () => {
    expect(tokenAtCursor("tag:focus and status:wip", 24)).toMatchObject({
      start: 14,
      end: 24,
      text: "status:wip",
    });
  });

  it("stops at the caret rather than at the end of the token", () => {
    expect(tokenAtCursor("tag:focus", 6)).toMatchObject({ start: 0, end: 6, text: "tag:fo" });
  });

  it("treats an open wikilink as one token so its spaces do not break it", () => {
    expect(tokenAtCursor("near:[[Deep Work", 16)).toMatchObject({
      start: 5,
      end: 16,
      text: "[[Deep Work",
      wikilink: true,
    });
  });

  it("keeps a closed wikilink inside the atom that carries its hop bound", () => {
    expect(tokenAtCursor("near:[[Deep Work]]<=2", 21)).toMatchObject({
      start: 0,
      wikilink: false,
      text: "near:[[Deep Work]]<=2",
    });
    expect(completionContextAt("near:[[Deep Work]]<=2", 21)).toMatchObject({
      kind: "value",
      field: "near",
      arg: "[[Deep Work]]",
      prefix: "2",
    });
  });

  it("treats an open quote as one token", () => {
    expect(tokenAtCursor('title:"deep wo', 14)).toMatchObject({
      start: 6,
      text: '"deep wo',
      quoted: true,
    });
  });

  it("breaks on a parenthesis", () => {
    expect(tokenAtCursor("(tag:focus", 10)).toMatchObject({ start: 1, text: "tag:focus" });
  });

  it("returns an empty token after a space", () => {
    expect(tokenAtCursor("tag:focus and ", 14)).toMatchObject({ start: 14, end: 14, text: "" });
  });
});

describe("field versus value decision", () => {
  it("calls a bare partial token a field", () => {
    expect(completionContextAt("ta", 2)).toMatchObject({
      kind: "field",
      prefix: "ta",
      replace: { start: 0, end: 2 },
    });
  });

  it("calls the text after a colon a value for that field", () => {
    expect(completionContextAt("tag:fo", 6)).toMatchObject({
      kind: "value",
      field: "tag",
      arg: "",
      prefix: "fo",
      replace: { start: 4, end: 6 },
    });
  });

  it("reads the argument of prop:name=value", () => {
    expect(completionContextAt("prop:rating>=4", 14)).toMatchObject({
      kind: "value",
      field: "prop",
      arg: "rating",
      prefix: "4",
      replace: { start: 13, end: 14 },
    });
  });

  it("lower-cases the field so a shouted spelling still resolves", () => {
    expect(completionContextAt("TAG:fo", 6)).toMatchObject({ field: "tag", prefix: "fo" });
  });

  it("marks a wikilink value and strips the brackets from the prefix", () => {
    expect(completionContextAt("links-to:[[Deep", 15)).toMatchObject({
      kind: "value",
      field: "links-to",
      prefix: "Deep",
      wikilink: true,
      replace: { start: 9, end: 15 },
    });
  });

  it("marks a quoted value and unescapes the prefix", () => {
    expect(completionContextAt('tag:"a \\"b', 10)).toMatchObject({
      kind: "value",
      field: "tag",
      prefix: 'a "b',
      quoted: true,
      replace: { start: 4, end: 10 },
    });
  });

  it("offers fields after a boolean keyword rather than completing the keyword", () => {
    expect(completionContextAt("tag:focus and", 13)).toMatchObject({
      kind: "field",
      prefix: "",
      replace: { start: 10, end: 13 },
    });
  });

  it("treats an empty caret position as a field position", () => {
    expect(completionContextAt("", 0)).toMatchObject({
      kind: "field",
      prefix: "",
      replace: { start: 0, end: 0 },
    });
  });
});

describe("local filtering", () => {
  const list = [
    completion({ value: "focus" }),
    completion({ value: "deep-focus" }),
    completion({ value: "reading" }),
  ];

  it("puts prefix matches ahead of substring matches", () => {
    expect(filterCompletions(list, "foc").map((item) => item.value)).toEqual([
      "focus",
      "deep-focus",
    ]);
  });

  it("matches case- and accent-insensitively", () => {
    const accented = [completion({ value: "Café" })];
    expect(filterCompletions(accented, "café").map((item) => item.value)).toEqual(["Café"]);
  });

  it("returns a copy of the list for an empty prefix", () => {
    const all = filterCompletions(list, "  ");
    expect(all).toEqual(list);
    expect(all).not.toBe(list);
  });
});

describe("merging candidate lists", () => {
  it("keeps the first spelling of a repeated value and drops the duplicate", () => {
    const merged = mergeCompletions([
      [completion({ value: "Focus", count: 4 })],
      [completion({ value: "focus", count: 1 }), completion({ value: "reading" })],
    ]);
    expect(merged.map((item) => [item.value, item.count])).toEqual([
      ["Focus", 4],
      ["reading", 0],
    ]);
  });

  it("keeps the same value under two different kinds", () => {
    const merged = mergeCompletions([
      [completion({ value: "type", kind: "field" })],
      [completion({ value: "type", kind: "property" })],
    ]);
    expect(merged).toHaveLength(2);
  });

  it("stops at the limit", () => {
    const many = Array.from({ length: 8 }, (_, index) => completion({ value: `tag-${index}` }));
    expect(mergeCompletions([many], 3)).toHaveLength(3);
  });
});

describe("applying a completion", () => {
  it("splices the insert over the replace range and reports the caret", () => {
    expect(applyCompletion("tag:fo", { start: 4, end: 6 }, "focus")).toEqual({
      text: "tag:focus",
      pos: 9,
    });
  });

  it("leaves the caret after the colon a field insert carries", () => {
    expect(applyCompletion("ta", { start: 0, end: 2 }, "tag:")).toEqual({
      text: "tag:",
      pos: 4,
    });
  });

  it("preserves the text after the caret", () => {
    expect(applyCompletion("tag:fo and status:wip", { start: 4, end: 6 }, "focus")).toEqual({
      text: "tag:focus and status:wip",
      pos: 9,
    });
  });

  it("replaces a whole open wikilink with its closed form", () => {
    const context = completionContextAt("links-to:[[Deep", 15);
    expect(applyCompletion("links-to:[[Deep", context.replace, "[[Deep Work]]")).toEqual({
      text: "links-to:[[Deep Work]]",
      pos: 22,
    });
  });

  it("clamps a range that overruns the text", () => {
    expect(applyCompletion("tag", { start: 99, end: 120 }, "!")).toEqual({
      text: "tag!",
      pos: 4,
    });
  });
});

describe("completion label", () => {
  it("joins the value, its count, and its detail", () => {
    expect(completionLabel(completion({ value: "focus", count: 12, detail: "tag" })))
      .toBe("focus (12) — tag");
    expect(completionLabel(completion({ value: "focus" }))).toBe("focus");
  });
});
