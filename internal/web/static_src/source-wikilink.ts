import type { SearchResult } from "./api";

// Wikilink completion, as two pure questions: is the caret inside an unclosed
// `[[`, and what should the vault's search results offer there.
//
// Keeping them out of the CodeMirror adapter is what makes them testable at
// all. A completion source is called by the editor with an editor-shaped
// context; these two functions take a string and an offset.

export type WikilinkContext = {
  // from is the offset just after the `[[`, which is where a completion
  // replaces up to the caret.
  from: number;
  query: string;
};

export type WikilinkOption = {
  // label is the text inserted, and it is the *wikilink target* rather than
  // the title, because that is what the graph resolves.
  label: string;
  // detail is what distinguishes two notes whose targets look alike in a
  // dropdown: the title when it differs from the target's last segment.
  detail: string;
};

// wikilinkContextAt finds the open `[[` the caret sits inside, if any.
//
// It refuses to look past a newline: an unclosed `[[` on an earlier line is a
// typo somewhere else in the note, not an invitation to complete here. It also
// refuses once a `]]` has intervened, which is what keeps the caret after a
// finished `[[Alpha]]` from reopening it.
export const wikilinkContextAt = (doc: string, pos: number): WikilinkContext | null => {
  const caret = Math.min(Math.max(0, pos), doc.length);
  const lineStart = doc.lastIndexOf("\n", caret - 1) + 1;
  const line = doc.slice(lineStart, caret);

  const open = line.lastIndexOf("[[");
  if (open === -1) {
    return null;
  }
  const query = line.slice(open + 2);
  if (query.includes("]") || query.includes("[")) {
    return null;
  }
  return { from: lineStart + open + 2, query };
};

// wikilinkOptions turns search results into what the dropdown shows.
//
// A result with no wikilink is dropped rather than guessed at. The field is
// published by the server precisely because the browser cannot derive it — the
// key is folded and the title is frontmatter — so falling back to either one
// would insert a link that resolves to the wrong note or to nothing, which is
// worse than offering no completion.
export const wikilinkOptions = (results: SearchResult[]): WikilinkOption[] => {
  const seen = new Set<string>();
  return results.flatMap((result) => {
    const label = (result.wikilink || "").trim();
    if (label === "" || seen.has(label)) {
      return [];
    }
    seen.add(label);
    return [{ label, detail: wikilinkDetail(label, result.title) }];
  });
};

// wikilinkDetail says the title only when it adds something. A note whose title
// is its filename would otherwise show the same word twice in every row.
const wikilinkDetail = (label: string, title: string): string => {
  const trimmed = (title || "").trim();
  if (trimmed === "") {
    return "";
  }
  const lastSegment = label.slice(label.lastIndexOf("/") + 1);
  return trimmed === lastSegment ? "" : trimmed;
};

// wikilinkSearchURL builds the request the completion makes.
//
// The limit is small on purpose: this is a dropdown somebody is reading while
// typing, and a completion list longer than a screen is a list nobody scrolls.
export const wikilinkSearchURL = (endpoint: string, query: string, limit = 12): string => {
  const params = new URLSearchParams();
  params.set("q", query);
  params.set("limit", String(limit));
  return `${endpoint}?${params.toString()}`;
};
