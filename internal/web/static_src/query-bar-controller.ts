import type { QueryCompletion } from "./api";
import { queryElement } from "./dom";
import { dispatchFiltersChange, readExprParam, syncExprParam } from "./filters";
import {
  applyCompletion,
  completionContextAt,
  filterCompletions,
  mergeCompletions,
  type QueryCompletionContext,
} from "./query-bar-completions";
import {
  completionsDebounceMs,
  createDebouncer,
  emptyQueryBarValidation,
  isBlankQuery,
  nextActiveIndex,
  publishableQuery,
  reduceQueryBar,
  shouldRequestCompletions,
  validateDebounceMs,
  windowScheduler,
  type CompletionCause,
  type FetchLike,
  type QueryBarValidation,
  type Scheduler,
} from "./query-bar-model";
import { createQueryRequester } from "./query-bar-requests";
import {
  closeQueryBarCompletions,
  renderQueryBarCompletions,
  renderQueryBarError,
  renderQueryBarStatus,
  type QueryBarElements,
} from "./query-bar-render";

export type QueryBarDeps = {
  fetch?: FetchLike;
  scheduler?: Scheduler;
};

// The `expr` parameter and its URL read/write helpers live in filters.ts, next
// to the shared filter plumbing that carries the parameter into every API URL.
// One writer, one reader — the bar and the graph cannot disagree about what the
// URL says.
export { queryExprParam, readExprParam, syncExprParam } from "./filters";

export const resolveQueryBarElements = (doc: Document): QueryBarElements | null => {
  const root = queryElement(doc, "[data-query-bar]", HTMLElement);
  if (!root) {
    return null;
  }
  const input = queryElement(root, "[data-query-bar-input]", HTMLInputElement);
  if (!input) {
    return null;
  }
  const errorEl = queryElement(root, "[data-query-bar-error]", HTMLElement);
  return {
    root,
    input,
    statusEl: queryElement(root, "[data-query-bar-status]", HTMLElement),
    errorEl,
    errorMessageEl: errorEl
      ? queryElement(errorEl, "[data-query-bar-error-message]", HTMLElement)
      : null,
    errorQueryEl: errorEl
      ? queryElement(errorEl, "[data-query-bar-error-query]", HTMLElement)
      : null,
    errorCaretEl: errorEl
      ? queryElement(errorEl, "[data-query-bar-error-caret]", HTMLElement)
      : null,
    suggestionsEl: errorEl
      ? queryElement(errorEl, "[data-query-bar-error-suggestions]", HTMLElement)
      : null,
    listEl: queryElement(root, "[data-query-bar-completions]", HTMLElement),
    clearButton: queryElement(root, "[data-query-bar-clear]", HTMLButtonElement),
  };
};

/**
 * Wires the query bar to the DOM: validation on a debounce, completions on a
 * shorter one, and keyboard navigation over the listbox.
 *
 * The pure rules all live in query-bar-model.ts and query-bar-completions.ts;
 * what is here is the part that cannot be tested without a document.
 */
export const initQueryBarController = (
  els: QueryBarElements,
  deps: QueryBarDeps = {}
): void => {
  const fetcher = deps.fetch || ((input, init) => fetch(input, init));
  const scheduler = deps.scheduler || windowScheduler(window);
  const requester = createQueryRequester(fetcher);
  const validateDebounce = createDebouncer(scheduler);
  const completionsDebounce = createDebouncer(scheduler);

  let validation: QueryBarValidation = emptyQueryBarValidation();
  let completions: QueryCompletion[] = [];
  let activeIndex = -1;
  let listOpen = false;
  let published = readExprParam();

  const renderList = (): void => {
    if (!listOpen) {
      closeQueryBarCompletions(els);
      return;
    }
    renderQueryBarCompletions(els, completions, {
      activeIndex,
      onChoose: (completion) => {
        acceptCompletion(completion);
      },
    });
  };

  const closeList = (): void => {
    listOpen = false;
    completions = [];
    activeIndex = -1;
    completionsDebounce.cancel();
    renderList();
  };

  const renderAll = (): void => {
    renderQueryBarStatus(els, validation);
    renderQueryBarError(els, validation);
  };

  /**
   * Publishes a confirmed expression: the URL gains `expr`, and the dispatch is
   * what makes every listener on the page — the Library results, the graph —
   * refetch through `buildFilteredAPIURL`, which carries the parameter.
   *
   * `mode: ""` reaches every listener regardless of its facet mode, because the
   * expression is the page's outermost filter rather than one panel's state.
   */
  const publish = (expr: string): void => {
    if (expr === published) {
      return;
    }
    published = expr;
    syncExprParam(expr);
    dispatchFiltersChange(window, {
      mode: "",
      params: new URLSearchParams(window.location.search),
    });
  };

  const runValidate = (text: string): void => {
    validation = reduceQueryBar(validation, { type: "request", text });
    renderAll();
    void requester
      .validate(text)
      .then((response) => {
        if (response === null) {
          return;
        }
        validation = reduceQueryBar(validation, { type: "response", text, response });
        renderAll();
        const confirmed = publishableQuery(validation);
        if (confirmed !== null) {
          publish(confirmed);
        }
      })
      .catch(() => {
        validation = reduceQueryBar(validation, { type: "failure", text });
        renderAll();
      });
  };

  const applyCompletionsResponse = (
    context: QueryCompletionContext,
    incoming: QueryCompletion[]
  ): void => {
    // The reply describes the text as it was when the request went out. Both
    // the fresh list and the rows already on screen are narrowed to the token
    // under the caret *now*, so keystrokes that landed in flight narrow the
    // popup instead of resurrecting rows. Merging the two — fresh first — is
    // what keeps the tail populated when the server's 50-row cap trims a
    // broader prefix differently than a narrower one.
    completions = mergeCompletions([
      filterCompletions(incoming, context.prefix),
      filterCompletions(completions, context.prefix),
    ]);
    activeIndex = completions.length > 0 ? 0 : -1;
    listOpen = completions.length > 0;
    renderList();
  };

  const runCompletions = (text: string, pos: number): void => {
    void requester
      .completions(text, pos)
      .then((response) => {
        if (response === null) {
          return;
        }
        const context = completionContextAt(els.input.value, caretPosition());
        applyCompletionsResponse(context, response.completions);
      })
      .catch(() => {
        closeList();
      });
  };

  const caretPosition = (): number => {
    const selection = els.input.selectionStart;
    return typeof selection === "number" ? selection : els.input.value.length;
  };

  const acceptCompletion = (completion: QueryCompletion): void => {
    const text = els.input.value;
    const context = completionContextAt(text, caretPosition());
    const edit = applyCompletion(text, context.replace, completion.insert);
    els.input.value = edit.text;
    els.input.setSelectionRange(edit.pos, edit.pos);
    els.input.focus();
    closeList();
    // Accepting a field name leaves the caret just past its ":", where the
    // value list is the obvious next thing to show. Accepting a value finishes
    // the token, so the popup stays shut and only the validation runs.
    handleTextChange(edit.text, {
      immediate: true,
      completionCause: context.kind === "field" ? "manual" : null,
    });
  };

  const requestCompletions = (text: string, cause: CompletionCause): void => {
    const pos = caretPosition();
    if (!shouldRequestCompletions(text, pos, cause)) {
      closeList();
      return;
    }
    // The rows already on screen narrow immediately, so the popup tracks the
    // keystroke rather than the round trip.
    if (listOpen) {
      const context = completionContextAt(text, pos);
      completions = filterCompletions(completions, context.prefix);
      activeIndex = completions.length > 0 ? Math.min(activeIndex, completions.length - 1) : -1;
      renderList();
    }
    completionsDebounce.run(() => {
      runCompletions(els.input.value, caretPosition());
    }, cause === "manual" ? 0 : completionsDebounceMs);
  };

  type TextChangeOptions = {
    immediate?: boolean;
    /** null suppresses the completion request entirely. */
    completionCause?: CompletionCause | null;
  };

  const handleTextChange = (
    text: string,
    { immediate = false, completionCause = "input" }: TextChangeOptions = {}
  ): void => {
    validation = reduceQueryBar(validation, { type: "input", text });
    renderAll();

    if (isBlankQuery(text)) {
      validateDebounce.cancel();
      requester.cancel();
      publish("");
      if (completionCause === "focus" || completionCause === "manual") {
        requestCompletions(text, completionCause);
      } else {
        closeList();
      }
      return;
    }

    validateDebounce.run(() => {
      runValidate(els.input.value);
    }, immediate ? 0 : validateDebounceMs);
    if (completionCause !== null) {
      requestCompletions(text, completionCause);
    }
  };

  els.input.addEventListener("input", () => {
    handleTextChange(els.input.value);
  });

  els.input.addEventListener("focus", () => {
    if (els.input.value === "") {
      requestCompletions("", "focus");
    }
  });

  els.input.addEventListener("blur", () => {
    closeList();
  });

  els.input.addEventListener("keydown", (event) => {
    if (event.key === " " && event.ctrlKey) {
      event.preventDefault();
      requestCompletions(els.input.value, "manual");
      return;
    }
    if (event.key === "Escape") {
      if (listOpen) {
        event.preventDefault();
        closeList();
      }
      return;
    }
    if (!listOpen || completions.length === 0) {
      if (event.key === "Enter") {
        event.preventDefault();
        validateDebounce.run(() => {
          runValidate(els.input.value);
        }, 0);
      }
      return;
    }

    switch (event.key) {
      case "ArrowDown":
        event.preventDefault();
        activeIndex = nextActiveIndex(activeIndex, 1, completions.length);
        renderList();
        return;
      case "ArrowUp":
        event.preventDefault();
        activeIndex = nextActiveIndex(activeIndex, -1, completions.length);
        renderList();
        return;
      case "Enter":
      case "Tab": {
        const completion = completions[activeIndex];
        if (!completion) {
          return;
        }
        event.preventDefault();
        acceptCompletion(completion);
        return;
      }
      default:
    }
  });

  els.clearButton?.addEventListener("click", () => {
    els.input.value = "";
    validateDebounce.cancel();
    requester.cancel();
    validation = reduceQueryBar(validation, { type: "reset" });
    renderAll();
    closeList();
    publish("");
    els.input.focus();
  });

  // A URL that already carries an expression is re-validated on load rather
  // than trusted, so a hand-edited or stale link reports itself.
  const initial = readExprParam();
  if (initial !== "") {
    els.input.value = initial;
    validation = reduceQueryBar(validation, { type: "input", text: initial });
    renderAll();
    runValidate(initial);
    return;
  }
  renderAll();
};
