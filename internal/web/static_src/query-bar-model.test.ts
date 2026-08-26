import { describe, expect, it } from "vitest";

import { normalizeQueryCompletionsResponse, normalizeQueryValidateResponse } from "./api";
import {
  createDebouncer,
  emptyQueryBarValidation,
  errorCaretRow,
  isBlankQuery,
  nextActiveIndex,
  publishableQuery,
  queryBarStatus,
  reduceQueryBar,
  shouldRequestCompletions,
  type QueryBarValidation,
} from "./query-bar-model";
import {
  invalidValidatePayload,
  manualScheduler,
  validatePayload,
} from "./query-bar.test-helpers";

const typed = (text: string): QueryBarValidation =>
  reduceQueryBar(emptyQueryBarValidation(), { type: "input", text });

describe("query bar validation state machine", () => {
  it("starts empty and stays empty for a blank query", () => {
    expect(emptyQueryBarValidation().phase).toBe("empty");
    expect(typed("   ").phase).toBe("empty");
    expect(isBlankQuery("\t ")).toBe(true);
  });

  it("moves typing -> checking -> valid for one text", () => {
    let state = typed("tag:focus");
    expect(state.phase).toBe("typing");

    state = reduceQueryBar(state, { type: "request", text: "tag:focus" });
    expect(state.phase).toBe("checking");

    state = reduceQueryBar(state, {
      type: "response",
      text: "tag:focus",
      response: normalizeQueryValidateResponse(validatePayload({ needs_graph: true })),
    });
    expect(state.phase).toBe("valid");
    expect(state.normalized).toBe("tag:focus");
    expect(state.needsGraph).toBe(true);
    expect(state.error).toBeNull();
    expect(publishableQuery(state)).toBe("tag:focus");
  });

  it("ignores a response for text the bar has moved past", () => {
    let state = reduceQueryBar(typed("tag:foc"), { type: "request", text: "tag:foc" });
    state = reduceQueryBar(state, { type: "input", text: "tag:focus" });

    const stale = reduceQueryBar(state, {
      type: "response",
      text: "tag:foc",
      response: normalizeQueryValidateResponse(validatePayload({ query: "tag:foc" })),
    });

    expect(stale).toBe(state);
    expect(stale.phase).toBe("typing");
    expect(publishableQuery(stale)).toBeNull();
  });

  it("drops the previous error as soon as the text changes", () => {
    let state = reduceQueryBar(typed("statuz:wip"), {
      type: "response",
      text: "statuz:wip",
      response: normalizeQueryValidateResponse(invalidValidatePayload("statuz:wip")),
    });
    expect(state.phase).toBe("invalid");
    expect(state.error?.message).toBe("unknown field");

    state = reduceQueryBar(state, { type: "input", text: "status:wip" });
    expect(state.phase).toBe("typing");
    expect(state.error).toBeNull();
  });

  it("reports a failed request as unavailable, not invalid", () => {
    const state = reduceQueryBar(typed("tag:focus"), { type: "failure", text: "tag:focus" });
    expect(state.phase).toBe("unavailable");
    expect(queryBarStatus(state)).toEqual({
      message: "Expression check unavailable.",
      kind: "is-warning",
    });
    expect(publishableQuery(state)).toBeNull();
  });

  it("only publishes a confirmed query", () => {
    expect(publishableQuery(typed("tag:focus"))).toBeNull();
    expect(publishableQuery(emptyQueryBarValidation())).toBeNull();
  });

  it("resets to empty", () => {
    const state = reduceQueryBar(typed("tag:focus"), { type: "reset" });
    expect(state).toEqual(emptyQueryBarValidation());
  });
});

describe("query bar status text", () => {
  it("names the link graph only when the query needs it", () => {
    const withGraph = reduceQueryBar(typed("orphan:true"), {
      type: "response",
      text: "orphan:true",
      response: normalizeQueryValidateResponse(
        validatePayload({ query: "orphan:true", needs_graph: true })
      ),
    });
    expect(queryBarStatus(withGraph).message).toBe("Expression is valid. Uses the link graph.");

    const without = reduceQueryBar(typed("tag:focus"), {
      type: "response",
      text: "tag:focus",
      response: normalizeQueryValidateResponse(validatePayload()),
    });
    expect(queryBarStatus(without)).toEqual({
      message: "Expression is valid.",
      kind: "is-success",
    });
  });

  it("shows the server message for an invalid query", () => {
    const state = reduceQueryBar(typed("tag:"), {
      type: "response",
      text: "tag:",
      response: normalizeQueryValidateResponse(
        invalidValidatePayload("tag:", { message: "expected a value" })
      ),
    });
    expect(queryBarStatus(state)).toEqual({
      message: "expected a value",
      kind: "is-error",
    });
  });

  it("reports a check in flight", () => {
    expect(queryBarStatus(typed("tag:focus")).kind).toBe("is-loading");
    expect(queryBarStatus(emptyQueryBarValidation()).kind).toBe("");
  });
});

describe("error caret row", () => {
  const positioned = (query: string, start: number, end: number) =>
    normalizeQueryValidateResponse(
      invalidValidatePayload(query, { start, end, column: start + 1 })
    ).error;

  it("puts the caret under the offending span", () => {
    const query = "tag:focus and statuz:wip";
    expect(errorCaretRow(query, positioned(query, 14, 20))).toBe(`${" ".repeat(14)}^^^^^^`);
  });

  it("draws one caret for a zero-width span at the end of input", () => {
    const query = "tag:";
    expect(errorCaretRow(query, positioned(query, 4, 4))).toBe("    ^");
  });

  it("counts an accented character as the one UTF-16 unit the server reported", () => {
    // "café" is four code units, so a caret on the token after it starts at 5.
    const query = "café statuz";
    expect(errorCaretRow(query, positioned(query, 5, 11))).toBe(`${" ".repeat(5)}^^^^^^`);
  });

  it("echoes a tab so the caret row stays aligned with the query row", () => {
    const query = "tag:a\tstatuz:b";
    expect(errorCaretRow(query, positioned(query, 6, 12))).toBe("     \t^^^^^^");
  });

  it("draws nothing for an unpositioned error", () => {
    const error = normalizeQueryValidateResponse(
      invalidValidatePayload("tag:focus", { positioned: false })
    ).error;
    expect(errorCaretRow("tag:focus", error)).toBe("");
    expect(errorCaretRow("tag:focus", null)).toBe("");
  });
});

describe("completion trigger", () => {
  it("always asks when summoned by hand", () => {
    expect(shouldRequestCompletions("", 0, "manual")).toBe(true);
    expect(shouldRequestCompletions("tag:focus ", 10, "manual")).toBe(true);
  });

  it("asks on focus only for an empty bar", () => {
    expect(shouldRequestCompletions("", 0, "focus")).toBe(true);
    expect(shouldRequestCompletions("tag:focus", 9, "focus")).toBe(false);
  });

  it("asks while typing a token but not just after a space", () => {
    expect(shouldRequestCompletions("ta", 2, "input")).toBe(true);
    expect(shouldRequestCompletions("tag:", 4, "input")).toBe(true);
    expect(shouldRequestCompletions("tag:focus and ", 14, "input")).toBe(false);
    expect(shouldRequestCompletions("", 0, "input")).toBe(false);
  });

  it("ignores a caret outside the text", () => {
    expect(shouldRequestCompletions("tag", 9, "input")).toBe(false);
    expect(shouldRequestCompletions("tag", -1, "input")).toBe(false);
  });
});

describe("active index navigation", () => {
  it("wraps in both directions", () => {
    expect(nextActiveIndex(-1, 1, 3)).toBe(0);
    expect(nextActiveIndex(-1, -1, 3)).toBe(2);
    expect(nextActiveIndex(2, 1, 3)).toBe(0);
    expect(nextActiveIndex(0, -1, 3)).toBe(2);
  });

  it("activates nothing in an empty list", () => {
    expect(nextActiveIndex(0, 1, 0)).toBe(-1);
  });
});

describe("debouncer", () => {
  it("keeps only the last scheduled call", () => {
    const scheduler = manualScheduler();
    const debouncer = createDebouncer(scheduler);
    const seen: string[] = [];

    debouncer.run(() => seen.push("first"), 10);
    debouncer.run(() => seen.push("second"), 10);
    expect(scheduler.pending()).toBe(1);

    scheduler.flush();
    expect(seen).toEqual(["second"]);
  });

  it("runs synchronously at delay zero and cancels a pending call", () => {
    const scheduler = manualScheduler();
    const debouncer = createDebouncer(scheduler);
    const seen: string[] = [];

    debouncer.run(() => seen.push("pending"), 10);
    debouncer.run(() => seen.push("now"), 0);
    expect(seen).toEqual(["now"]);
    expect(scheduler.pending()).toBe(0);

    debouncer.run(() => seen.push("later"), 10);
    debouncer.cancel();
    scheduler.flush();
    expect(seen).toEqual(["now"]);
  });
});

describe("query payload normalizers", () => {
  it("clamps offsets that overrun the query", () => {
    const payload = normalizeQueryValidateResponse(
      invalidValidatePayload("tag:", { start: 99, end: 120, line: 0, column: 0 })
    );
    expect(payload.error).toEqual({
      message: "unknown field",
      start: 4,
      end: 4,
      line: 1,
      column: 1,
      positioned: true,
      suggestions: [],
    });
  });

  it("drops an error object that arrives alongside ok", () => {
    const payload = normalizeQueryValidateResponse({
      ...validatePayload(),
      error: { message: "stale", positioned: true },
    });
    expect(payload.ok).toBe(true);
    expect(payload.error).toBeNull();
  });

  it("keeps trailing whitespace in the echoed query so offsets still line up", () => {
    const payload = normalizeQueryValidateResponse(validatePayload({ query: "tag:focus  " }));
    expect(payload.query).toBe("tag:focus  ");
  });

  it("skips completion rows with nothing to insert", () => {
    const payload = normalizeQueryCompletionsResponse({
      ok: true,
      query: "tag:",
      kind: "value",
      completions: [
        { value: "focus", insert: "focus", kind: "tag", count: "3" },
        { value: "", insert: "" },
        "not-a-record",
      ],
    });
    expect(payload.completions).toEqual([
      { value: "focus", insert: "focus", kind: "tag", detail: "", count: 3 },
    ]);
    expect(payload.kind).toBe("value");
  });

  it("falls back to the display value when insert is missing", () => {
    const payload = normalizeQueryCompletionsResponse({
      ok: true,
      query: "",
      kind: "field",
      completions: [{ value: "tag" }],
    });
    expect(payload.completions[0]?.insert).toBe("tag");
    expect(payload.kind).toBe("field");
  });
});
