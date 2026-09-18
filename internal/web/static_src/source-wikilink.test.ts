import { describe, expect, it } from "vitest";

import { searchResult } from "./search-test-helpers";
import { wikilinkContextAt, wikilinkOptions, wikilinkSearchURL } from "./source-wikilink";

describe("wikilinkContextAt", () => {
  it("reports the query and where a completion replaces from", () => {
    const doc = "see [[Alp";
    expect(wikilinkContextAt(doc, doc.length)).toEqual({ from: 6, query: "Alp" });
  });

  it("offers an empty query the moment the brackets open", () => {
    const doc = "see [[";
    expect(wikilinkContextAt(doc, doc.length)).toEqual({ from: 6, query: "" });
  });

  it("is null with no open brackets", () => {
    expect(wikilinkContextAt("plain text", 5)).toBeNull();
  });

  // The caret after a finished link must not reopen it, or accepting a
  // completion would immediately offer another one over the text just written.
  it("is null once the link is closed", () => {
    const doc = "see [[Alpha]] and more";
    expect(wikilinkContextAt(doc, doc.length)).toBeNull();
  });

  // An unclosed `[[` on an earlier line is a typo somewhere else in the note,
  // not an invitation to complete here.
  it("does not look past a newline", () => {
    const doc = "[[stray\nnext line";
    expect(wikilinkContextAt(doc, doc.length)).toBeNull();
  });

  it("uses the innermost open brackets", () => {
    const doc = "[[one]] then [[tw";
    expect(wikilinkContextAt(doc, doc.length)).toEqual({ from: 15, query: "tw" });
  });

  it("clamps a position outside the document", () => {
    const doc = "[[ab";
    expect(wikilinkContextAt(doc, 999)).toEqual({ from: 2, query: "ab" });
    expect(wikilinkContextAt(doc, -4)).toBeNull();
  });
});

describe("wikilinkOptions", () => {
  // The whole reason the server publishes the field: the key is folded and the
  // title is frontmatter, so neither can build a link that both resolves and
  // reads correctly.
  it("inserts the published wikilink target rather than the key or the title", () => {
    const options = wikilinkOptions([
      searchResult({ key: "notes/renamed", title: "Something Else", wikilink: "Notes/Renamed" }),
    ]);
    expect(options).toEqual([{ label: "Notes/Renamed", detail: "Something Else" }]);
  });

  it("drops a result with no wikilink instead of guessing one", () => {
    expect(wikilinkOptions([searchResult({ key: "alpha", wikilink: "" })])).toEqual([]);
  });

  it("stays silent about a title that merely repeats the target's last segment", () => {
    const options = wikilinkOptions([
      searchResult({ title: "Renamed", wikilink: "Notes/Renamed" }),
    ]);
    expect(options).toEqual([{ label: "Notes/Renamed", detail: "" }]);
  });

  it("keeps the first of two results naming the same note", () => {
    const options = wikilinkOptions([
      searchResult({ title: "First", wikilink: "Top" }),
      searchResult({ title: "Second", wikilink: "Top" }),
    ]);
    expect(options).toEqual([{ label: "Top", detail: "First" }]);
  });
});

describe("wikilinkSearchURL", () => {
  it("escapes the query and carries a small limit", () => {
    expect(wikilinkSearchURL("/api/search", "a note & more", 5)).toBe(
      "/api/search?q=a+note+%26+more&limit=5"
    );
  });
});
