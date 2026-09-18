// The Markdown formatting commands, as pure text transforms.
//
// Nothing here knows what an editor is. Each command takes the document, a
// selection, and answers one replacement plus where the selection should end
// up; the surface applies it. That split is what lets the toolbar, the keymap
// and the tests all drive the same code — and it is why these are tested in
// Vitest rather than only in a browser, where every assertion would cost a
// page load.

export type SourceSelection = {
  from: number;
  to: number;
};

// SourceEdit replaces [from, to) with `insert` and leaves the selection at
// [selectionFrom, selectionTo). The two are separate because a command that
// wraps text wants the *text* selected afterwards, not the markers.
export type SourceEdit = SourceSelection & {
  insert: string;
  selectionFrom: number;
  selectionTo: number;
};

export type SourceFormatCommand =
  | "bold"
  | "italic"
  | "code"
  | "heading"
  | "bullet"
  | "link"
  | "wikilink";

// The inline markers, chosen so that toggling one never has to guess what
// another one meant.
//
// Emphasis is `_` rather than `*` deliberately. With `*` for italic and `**`
// for bold, `**bold**` both starts and ends with the italic marker, so a toggle
// cannot tell "already italic" from "already bold" without parsing — and the
// obvious implementation silently downgrades bold to italic. `_text_` and
// `**text**` share no prefix, so each toggle answers about itself.
const INLINE_MARKERS: Record<"bold" | "italic" | "code", string> = {
  bold: "**",
  italic: "_",
  code: "`",
};

const HEADING_PREFIX = /^(#{1,6}) /;
const BULLET_PREFIX = /^(\s*)([-*+]) /;

export type SourceFormatOptions = {
  // headingLevel is 1..6 and only read by the heading command. Applying the
  // level the line already carries removes it, so Ctrl+Alt+2 twice is a
  // round trip rather than two different broken states.
  headingLevel?: number;
};

export const formatSource = (
  doc: string,
  selection: SourceSelection,
  command: SourceFormatCommand,
  options: SourceFormatOptions = {}
): SourceEdit => {
  const range = clampSelection(doc, selection);
  switch (command) {
    case "bold":
    case "italic":
    case "code":
      return toggleInline(doc, range, INLINE_MARKERS[command]);
    case "heading":
      return toggleHeading(doc, range, headingLevel(options.headingLevel));
    case "bullet":
      return toggleBullet(doc, range);
    case "link":
      return insertLink(doc, range);
    case "wikilink":
      return insertWikilink(doc, range);
  }
};

// toggleInline wraps the selection, or unwraps it when it is already wrapped —
// whether the markers are inside the selection or just outside it. Both are
// reachable by ordinary use: selecting a word and pressing Ctrl+B twice leaves
// the markers outside the selection the second time.
const toggleInline = (doc: string, range: SourceSelection, marker: string): SourceEdit => {
  const selected = doc.slice(range.from, range.to);
  const width = marker.length;

  if (
    selected.length >= 2 * width &&
    selected.startsWith(marker) &&
    selected.endsWith(marker)
  ) {
    const inner = selected.slice(width, selected.length - width);
    return edit(range.from, range.to, inner, range.from, range.from + inner.length);
  }

  if (
    doc.slice(range.from - width, range.from) === marker &&
    doc.slice(range.to, range.to + width) === marker
  ) {
    return edit(
      range.from - width,
      range.to + width,
      selected,
      range.from - width,
      range.from - width + selected.length
    );
  }

  const insert = marker + selected + marker;
  return edit(
    range.from,
    range.to,
    insert,
    range.from + width,
    range.from + width + selected.length
  );
};

// toggleHeading rewrites every line the selection touches. Applying it to a
// multi-line selection is the only way to promote a run of lines at once, and
// applying it to a caret is the common case — the same code covers both because
// a caret spans exactly one line.
const toggleHeading = (doc: string, range: SourceSelection, level: number): SourceEdit => {
  const marker = "#".repeat(level);
  return rewriteLines(doc, range, (line) => {
    const existing = HEADING_PREFIX.exec(line);
    if (!existing) {
      return `${marker} ${line}`;
    }
    const body = line.slice(existing[0].length);
    return existing[1] === marker ? body : `${marker} ${body}`;
  });
};

// toggleBullet turns the touched lines into a list, or back out of one. It is
// all-or-nothing across the selection: a run where some lines are already
// bulleted becomes wholly bulleted, because the alternative — flipping each
// line independently — inverts a partly-marked list instead of finishing it.
const toggleBullet = (doc: string, range: SourceSelection): SourceEdit => {
  const lines = linesInRange(doc, range);
  const allBulleted = lines.every((line) => line.trim() === "" || BULLET_PREFIX.test(line));
  return rewriteLines(doc, range, (line) => {
    if (line.trim() === "") {
      return line;
    }
    const existing = BULLET_PREFIX.exec(line);
    if (allBulleted && existing) {
      return existing[1] + line.slice(existing[0].length);
    }
    return existing ? line : `- ${line}`;
  });
};

// insertLink writes a Markdown link and leaves the selection on the part that
// still needs typing: the destination when there was text to link, the text
// when there was not.
const insertLink = (doc: string, range: SourceSelection): SourceEdit => {
  const selected = doc.slice(range.from, range.to);
  if (selected === "") {
    const insert = "[text](url)";
    return edit(range.from, range.to, insert, range.from + 1, range.from + 5);
  }
  const insert = `[${selected}](url)`;
  const urlStart = range.from + selected.length + 3;
  return edit(range.from, range.to, insert, urlStart, urlStart + 3);
};

// insertWikilink writes the brackets and puts the caret between them, which is
// also where source-wikilink's completion starts offering notes.
const insertWikilink = (doc: string, range: SourceSelection): SourceEdit => {
  const selected = doc.slice(range.from, range.to);
  const insert = `[[${selected}]]`;
  return edit(
    range.from,
    range.to,
    insert,
    range.from + 2,
    range.from + 2 + selected.length
  );
};

const edit = (
  from: number,
  to: number,
  insert: string,
  selectionFrom: number,
  selectionTo: number
): SourceEdit => ({ from, to, insert, selectionFrom, selectionTo });

const headingLevel = (level: number | undefined): number => {
  if (typeof level !== "number" || !Number.isFinite(level)) {
    return 1;
  }
  return Math.min(6, Math.max(1, Math.trunc(level)));
};

const clampSelection = (doc: string, selection: SourceSelection): SourceSelection => {
  const from = Math.min(Math.max(0, selection.from), doc.length);
  const to = Math.min(Math.max(0, selection.to), doc.length);
  return from <= to ? { from, to } : { from: to, to: from };
};

// lineRange is the span of whole lines the selection touches. A selection
// ending exactly at a line start does not pull that line in — otherwise
// selecting one whole line with a trailing newline would format two.
export const lineRange = (doc: string, selection: SourceSelection): SourceSelection => {
  const range = clampSelection(doc, selection);
  const start = doc.lastIndexOf("\n", range.from - 1) + 1;
  const endSearchFrom = range.to > range.from ? range.to - 1 : range.to;
  const newline = doc.indexOf("\n", endSearchFrom);
  return { from: start, to: newline === -1 ? doc.length : newline };
};

const linesInRange = (doc: string, selection: SourceSelection): string[] => {
  const span = lineRange(doc, selection);
  return doc.slice(span.from, span.to).split("\n");
};

// rewriteLines replaces the touched lines and selects the result. Selecting the
// whole rewritten block rather than restoring the original offsets is what
// makes a repeated press idempotent: the second one sees the same lines.
const rewriteLines = (
  doc: string,
  selection: SourceSelection,
  rewrite: (line: string) => string
): SourceEdit => {
  const span = lineRange(doc, selection);
  const insert = doc.slice(span.from, span.to).split("\n").map(rewrite).join("\n");
  return edit(span.from, span.to, insert, span.from, span.from + insert.length);
};
