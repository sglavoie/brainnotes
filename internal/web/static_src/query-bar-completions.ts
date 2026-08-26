import type { QueryCompletion, QueryCompletionKind, QueryReplaceRange } from "./api";

/**
 * Token-at-cursor extraction and the field-vs-value decision, done locally.
 *
 * The server is authoritative — `/api/query/completions` runs the real lexer —
 * but a keystroke lands before the reply does, and a popup that cannot narrow
 * itself in the meantime flickers. These functions are the local mirror: good
 * enough to filter and label the list already on screen, never used to decide
 * what the candidates *are*.
 */

/** Characters that end a token outside of quotes or brackets. */
const tokenBreak = new Set([" ", "\t", "\n", "(", ")"]);

export type QueryToken = {
  start: number;
  end: number;
  text: string;
  /** True when the token opened a `"` phrase that the caret is still inside. */
  quoted: boolean;
  /** True when the token opened a `[[` wikilink the caret is still inside. */
  wikilink: boolean;
};

/**
 * Extracts the token the caret sits in, scanning left from the caret.
 *
 * A `[[` wikilink swallows spaces (`near:[[Deep Work` is one token) and so does
 * an open quote, which is why this cannot be a split on whitespace.
 */
export const tokenAtCursor = (text: string, pos: number): QueryToken => {
  const caret = Math.max(0, Math.min(Math.trunc(pos), text.length));
  const head = text.slice(0, caret);

  const quoteCount = countUnescapedQuotes(head);
  const quoted = quoteCount % 2 === 1;
  if (quoted) {
    const start = lastUnescapedQuote(head);
    return { start, end: caret, text: text.slice(start, caret), quoted: true, wikilink: false };
  }

  const openLink = head.lastIndexOf("[[");
  if (openLink >= 0 && head.indexOf("]]", openLink) < 0) {
    return {
      start: openLink,
      end: caret,
      text: text.slice(openLink, caret),
      quoted: false,
      wikilink: true,
    };
  }

  // A *closed* wikilink is part of the atom that contains it, spaces and all —
  // `near:[[Deep Work]]<=2` is one token whose hop bound is being typed. So the
  // leftward scan jumps over a `[[...]]` rather than stopping inside it.
  let start = caret;
  while (start > 0) {
    if (start >= 2 && text.slice(start - 2, start) === "]]") {
      const open = text.lastIndexOf("[[", start - 2);
      if (open >= 0) {
        start = open;
        continue;
      }
    }
    if (tokenBreak.has(text.charAt(start - 1))) {
      break;
    }
    start -= 1;
  }
  return { start, end: caret, text: text.slice(start, caret), quoted: false, wikilink: false };
};

const countUnescapedQuotes = (text: string): number => {
  let count = 0;
  for (let i = 0; i < text.length; i += 1) {
    const char = text.charAt(i);
    if (char === "\\") {
      i += 1;
      continue;
    }
    if (char === '"') {
      count += 1;
    }
  }
  return count;
};

const lastUnescapedQuote = (text: string): number => {
  let index = -1;
  for (let i = 0; i < text.length; i += 1) {
    const char = text.charAt(i);
    if (char === "\\") {
      i += 1;
      continue;
    }
    if (char === '"') {
      index = i;
    }
  }
  return index < 0 ? text.length : index;
};

/** The boolean words the language spells out, which are never field names. */
const keywords = new Set(["and", "or", "not"]);

/** Comparison operators that separate a field from the value it is compared to. */
const valueSeparators = [">=", "<=", "!=", "=", ":", ">", "<"];

export type QueryCompletionContext = {
  kind: QueryCompletionKind;
  /** The field whose values are being completed, lower-cased. */
  field: string;
  /** The field's argument, for `prop:<name>=` and `age:<name>`. */
  arg: string;
  /** The partial text typed so far, which the list is filtered by. */
  prefix: string;
  /** The span a chosen candidate's `insert` overwrites. */
  replace: QueryReplaceRange;
  quoted: boolean;
  wikilink: boolean;
};

/**
 * Decides locally whether the caret expects a field name or a value, and what
 * has been typed of it. Mirrors `queryexpr.CompletionAt`; the reply's own
 * `kind`/`field` win whenever one has arrived.
 */
export const completionContextAt = (text: string, pos: number): QueryCompletionContext => {
  const token = tokenAtCursor(text, pos);

  if (token.wikilink) {
    const head = text.slice(0, token.start);
    const owner = tokenAtCursor(head, head.length);
    const separated = splitFieldAndValue(owner.text);
    return {
      kind: "value",
      field: separated ? separated.field : "",
      arg: separated ? separated.arg : "",
      prefix: token.text.slice(2),
      replace: { start: token.start, end: token.end },
      quoted: false,
      wikilink: true,
    };
  }

  if (token.quoted) {
    const head = text.slice(0, token.start);
    const owner = tokenAtCursor(head, head.length);
    const separated = splitFieldAndValue(owner.text);
    return {
      kind: "value",
      field: separated ? separated.field : "",
      arg: separated ? separated.arg : "",
      prefix: unquote(token.text.slice(1)),
      replace: { start: token.start, end: token.end },
      quoted: true,
      wikilink: false,
    };
  }

  const separated = splitFieldAndValue(token.text);
  if (separated) {
    return {
      kind: "value",
      field: separated.field,
      arg: separated.arg,
      prefix: separated.value,
      replace: { start: token.start + separated.valueOffset, end: token.end },
      quoted: false,
      wikilink: false,
    };
  }

  return {
    kind: "field",
    field: "",
    arg: "",
    prefix: keywords.has(token.text.toLowerCase()) ? "" : token.text,
    replace: { start: token.start, end: token.end },
    quoted: false,
    wikilink: false,
  };
};

type SeparatedToken = {
  field: string;
  arg: string;
  value: string;
  /** Where the value starts inside the token. */
  valueOffset: number;
};

/**
 * Splits `field:value`, `prop:name=value`, and `age:name>=…` at their first
 * separator. Returns null when the token carries no separator at all, which is
 * what makes it a field position.
 */
const splitFieldAndValue = (token: string): SeparatedToken | null => {
  let index = -1;
  let width = 0;
  for (const separator of valueSeparators) {
    const found = token.indexOf(separator);
    if (found < 0) {
      continue;
    }
    if (index < 0 || found < index || (found === index && separator.length > width)) {
      index = found;
      width = separator.length;
    }
  }
  if (index < 0) {
    return null;
  }

  const field = token.slice(0, index).toLowerCase();
  const rest = token.slice(index + width);
  const restOffset = index + width;

  // A second separator means the first one carried an argument, which is how
  // `prop:rating>=4` and `age:updated<=7d` are spelled.
  let argIndex = -1;
  let argWidth = 0;
  for (const separator of valueSeparators) {
    const found = rest.indexOf(separator);
    if (found < 0) {
      continue;
    }
    if (argIndex < 0 || found < argIndex || (found === argIndex && separator.length > argWidth)) {
      argIndex = found;
      argWidth = separator.length;
    }
  }
  if (argIndex >= 0) {
    return {
      field,
      arg: rest.slice(0, argIndex),
      value: rest.slice(argIndex + argWidth),
      valueOffset: restOffset + argIndex + argWidth,
    };
  }

  return { field, arg: "", value: rest, valueOffset: restOffset };
};

const unquote = (text: string): string => text.replace(/\\(["\\])/g, "$1");

const foldCompletionValue = (value: string): string => value.trim().toLowerCase().normalize("NFC");

/**
 * Narrows a list to a prefix the way the server ranks one: prefix matches
 * first, then substring matches, order otherwise preserved. Used between
 * keystrokes so the popup responds before the next reply lands.
 */
export const filterCompletions = (
  completions: QueryCompletion[],
  prefix: string
): QueryCompletion[] => {
  const folded = foldCompletionValue(prefix);
  if (folded === "") {
    return completions.slice();
  }
  const leading: QueryCompletion[] = [];
  const contained: QueryCompletion[] = [];
  for (const completion of completions) {
    const candidate = foldCompletionValue(completion.value);
    if (candidate.startsWith(folded)) {
      leading.push(completion);
    } else if (candidate.includes(folded)) {
      contained.push(completion);
    }
  }
  return leading.concat(contained);
};

/**
 * Merges candidate lists, earlier lists winning. Facet values arrive from the
 * value response while the field list is still the one on screen, and a note
 * title can appear in both; deduping on the folded value is what keeps one row
 * per candidate rather than one per source.
 */
export const mergeCompletions = (
  lists: QueryCompletion[][],
  limit = 50
): QueryCompletion[] => {
  const seen = new Set<string>();
  const out: QueryCompletion[] = [];
  for (const list of lists) {
    for (const completion of list) {
      const key = `${completion.kind}\u0000${foldCompletionValue(completion.value)}`;
      if (seen.has(key)) {
        continue;
      }
      seen.add(key);
      out.push(completion);
      if (out.length >= limit) {
        return out;
      }
    }
  }
  return out;
};

export type QueryEdit = {
  text: string;
  pos: number;
};

/**
 * Splices a candidate's `insert` over the replace range and reports where the
 * caret lands. A field insert already carries its trailing ":", so the caret
 * ends up exactly where the value goes.
 */
export const applyCompletion = (
  text: string,
  replace: QueryReplaceRange,
  insert: string
): QueryEdit => {
  const start = Math.max(0, Math.min(replace.start, text.length));
  const end = Math.max(start, Math.min(replace.end, text.length));
  const next = `${text.slice(0, start)}${insert}${text.slice(end)}`;
  return { text: next, pos: start + insert.length };
};

export const completionLabel = (completion: QueryCompletion): string => {
  const detail = completion.detail.trim();
  const suffix = completion.count > 0 ? ` (${completion.count})` : "";
  return detail ? `${completion.value}${suffix} — ${detail}` : `${completion.value}${suffix}`;
};
