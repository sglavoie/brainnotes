import type { QueryValidateError, QueryValidateResponse } from "./api";
import type { StatusKind } from "./status";

/**
 * The pure half of the query bar: the validation state machine, the debounce
 * scheduler seam, and the completion-trigger rule. Nothing here touches the
 * DOM, `window`, or `fetch`, so every rule is unit-testable on its own.
 */

export type FetchLike = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;

/**
 * - `empty`: nothing typed, so nothing to say.
 * - `typing`: text changed and no request has gone out for it yet.
 * - `checking`: a validate request for exactly this text is in flight.
 * - `valid` / `invalid`: the server answered about exactly this text.
 * - `unavailable`: the request failed, which is not the same as invalid — a
 *   query bar that reports a network drop as a syntax error is lying.
 */
export type QueryBarPhase =
  | "empty"
  | "typing"
  | "checking"
  | "valid"
  | "invalid"
  | "unavailable";

export type QueryBarValidation = {
  phase: QueryBarPhase;
  /** The text this phase describes. Every offset in `error` indexes it. */
  query: string;
  normalized: string;
  fieldsUsed: string[];
  needsGraph: boolean;
  error: QueryValidateError | null;
};

export type QueryBarEvent =
  | { type: "input"; text: string }
  | { type: "request"; text: string }
  | { type: "response"; text: string; response: QueryValidateResponse }
  | { type: "failure"; text: string }
  | { type: "reset" };

export const emptyQueryBarValidation = (): QueryBarValidation => ({
  phase: "empty",
  query: "",
  normalized: "",
  fieldsUsed: [],
  needsGraph: false,
  error: null,
});

/** A query that is only whitespace filters nothing, so it is not "invalid". */
export const isBlankQuery = (text: string): boolean => text.trim() === "";

/**
 * Advances the validation state machine.
 *
 * `response` and `failure` are ignored unless they name the text the machine is
 * currently describing. The abort controller in query-bar-requests.ts already
 * drops superseded replies; this is the second half of the same guarantee, and
 * the one that matters, because an error's offsets index the text it was
 * computed from and would point at the wrong column on any other.
 */
const reduceInput = (state: QueryBarValidation, text: string): QueryBarValidation => {
  if (isBlankQuery(text)) {
    return emptyQueryBarValidation();
  }
  if (text === state.query && state.phase !== "empty") {
    return state;
  }
  // The previous error is dropped rather than carried: its offsets index
  // the previous text, so keeping it would drift the caret while typing.
  return {
    phase: "typing",
    query: text,
    normalized: "",
    fieldsUsed: [],
    needsGraph: false,
    error: null,
  };
};

const validatedState = (text: string, response: QueryValidateResponse): QueryBarValidation => ({
  phase: response.ok ? "valid" : "invalid",
  query: text,
  normalized: response.normalized,
  fieldsUsed: response.fields_used,
  needsGraph: response.needs_graph,
  error: response.ok ? null : response.error,
});

export const reduceQueryBar = (
  state: QueryBarValidation,
  event: QueryBarEvent
): QueryBarValidation => {
  switch (event.type) {
    case "reset":
      return emptyQueryBarValidation();

    case "input":
      return reduceInput(state, event.text);

    case "request":
      if (event.text !== state.query || isBlankQuery(event.text)) {
        return state;
      }
      return { ...state, phase: "checking" };

    case "response":
      return event.text === state.query ? validatedState(event.text, event.response) : state;

    case "failure":
      if (event.text !== state.query) {
        return state;
      }
      return { ...state, phase: "unavailable", error: null };

    default:
      return state;
  }
};

export type QueryBarStatus = {
  message: string;
  kind: StatusKind;
};

export const queryBarStatus = (state: QueryBarValidation): QueryBarStatus => {
  switch (state.phase) {
    case "empty":
      return { message: "Type an expression to filter the vault.", kind: "" };
    case "typing":
    case "checking":
      return { message: "Checking expression…", kind: "is-loading" };
    case "valid":
      return {
        message: state.needsGraph
          ? "Expression is valid. Uses the link graph."
          : "Expression is valid.",
        kind: "is-success",
      };
    case "invalid":
      return {
        message: state.error?.message || "Expression is not valid.",
        kind: "is-error",
      };
    case "unavailable":
      return { message: "Expression check unavailable.", kind: "is-warning" };
    default:
      return { message: "", kind: "" };
  }
};

/** Only a query the server confirmed may be published to the URL. */
export const publishableQuery = (state: QueryBarValidation): string | null =>
  state.phase === "valid" ? state.query : null;

/**
 * Builds the caret row that sits under the echoed query in the error strip.
 * Offsets are UTF-16 code units, which is what a JS string index already is, so
 * they slice directly. A zero-width span still gets one caret: pointing at the
 * end of input is how a truncated expression reports itself.
 */
export const errorCaretRow = (query: string, error: QueryValidateError | null): string => {
  if (!error || !error.positioned) {
    return "";
  }
  const start = Math.max(0, Math.min(error.start, query.length));
  const end = Math.max(start, Math.min(error.end, query.length));
  // A tab in the echoed row advances more than one column, so it is echoed as
  // a tab in the caret row too and the two stay aligned.
  const lead = Array.from(query.slice(0, start), (char) => (char === "\t" ? "\t" : " ")).join("");
  return `${lead}${"^".repeat(Math.max(1, end - start))}`;
};

export type CompletionCause = "input" | "focus" | "manual";

/**
 * Decides whether a keystroke should ask the server what can go here.
 *
 * `manual` (ctrl+space) always asks. `focus` asks only for an empty bar, where
 * the field list is the whole point. Typing asks unless the caret sits just
 * after whitespace, because the field list is not what someone who just pressed
 * space is looking at — they can still summon it with ctrl+space.
 */
export const shouldRequestCompletions = (
  text: string,
  pos: number,
  cause: CompletionCause
): boolean => {
  if (cause === "manual") {
    return true;
  }
  if (cause === "focus") {
    return text === "";
  }
  if (pos <= 0 || pos > text.length) {
    return false;
  }
  const previous = text.charAt(pos - 1);
  return previous.trim() !== "";
};

/** Wraps around, and returns -1 for an empty list so nothing is activated. */
export const nextActiveIndex = (current: number, delta: number, length: number): number => {
  if (length <= 0) {
    return -1;
  }
  if (current < 0) {
    return delta >= 0 ? 0 : length - 1;
  }
  return (((current + delta) % length) + length) % length;
};

export type Scheduler = {
  schedule: (fn: () => void, delay: number) => number;
  cancel: (handle: number) => void;
};

export type Debouncer = {
  run: (fn: () => void, delay: number) => void;
  cancel: () => void;
};

/**
 * A debouncer over an injected scheduler. Taking the timer functions as a
 * dependency is what keeps the debounce rule testable without `window` and
 * without waiting on real time.
 */
export const createDebouncer = (scheduler: Scheduler): Debouncer => {
  let handle: number | null = null;

  const cancel = (): void => {
    if (handle !== null) {
      scheduler.cancel(handle);
      handle = null;
    }
  };

  return {
    cancel,
    run: (fn, delay) => {
      cancel();
      if (delay <= 0) {
        fn();
        return;
      }
      handle = scheduler.schedule(() => {
        handle = null;
        fn();
      }, delay);
    },
  };
};

export const windowScheduler = (target: Window): Scheduler => ({
  schedule: (fn, delay) => target.setTimeout(fn, delay),
  cancel: (handle) => target.clearTimeout(handle),
});

/** Debounce windows, in milliseconds. */
export const validateDebounceMs = 180;
export const completionsDebounceMs = 120;
