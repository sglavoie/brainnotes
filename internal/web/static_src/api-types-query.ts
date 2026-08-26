/**
 * Shapes returned by /api/query/validate and /api/query/completions.
 *
 * Every offset on these types — `start`, `end`, `column`, `pos`, and the
 * `replace` range — is a UTF-16 code-unit index into `query`, which is exactly
 * how a browser indexes an <input> value. They can be handed to
 * `setSelectionRange` or used to slice the string with no conversion.
 */

export type QueryValidateError = {
  message: string;
  start: number;
  end: number;
  /** 1-based. */
  line: number;
  /** 1-based, in UTF-16 code units from the start of the line. */
  column: number;
  /**
   * False when the failure carries no span into the query (a snapshot error,
   * say). The offsets are still present so one decoder serves both, but a
   * caret must not be drawn from them.
   */
  positioned: boolean;
  suggestions: string[];
};

export type QueryValidateResponse = {
  ok: boolean;
  /** The expression exactly as sent, since every offset indexes it. */
  query: string;
  /** The canonical spelling of a valid query. Empty when invalid. */
  normalized: string;
  /** Populated whenever the query parses, even if it then fails to compile. */
  fields_used: string[];
  needs_graph: boolean;
  error: QueryValidateError | null;
};

/** What the caret position expects: a field name or a value for one. */
export type QueryCompletionKind = "field" | "value";

export type QueryCompletion = {
  /** The bare candidate, for display. */
  value: string;
  /** The query text to splice over the replace range. */
  insert: string;
  /** Where the candidate came from: "field", "property", "tag", "note", ... */
  kind: string;
  detail: string;
  count: number;
};

export type QueryReplaceRange = {
  start: number;
  end: number;
};

export type QueryCompletionsResponse = {
  ok: boolean;
  query: string;
  kind: QueryCompletionKind;
  /** The field whose values are being completed. Empty at a field position. */
  field: string;
  /** The field's argument, for `prop:<name>=` and `age:<name>`. */
  arg: string;
  /** The partial token the candidates were filtered by. */
  prefix: string;
  pos: number;
  replace: QueryReplaceRange;
  completions: QueryCompletion[];
};
