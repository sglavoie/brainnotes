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
    retryButton: queryElement(root, "[data-query-bar-retry]", HTMLButtonElement),
  };
};

const isCompositionKey = (event: KeyboardEvent): boolean => event.isComposing || event.keyCode === 229;

type TextChangeOptions = {
  immediate?: boolean;
  completionCause?: CompletionCause | null;
};

class QueryBarController {
  private requester: ReturnType<typeof createQueryRequester>;
  private validateDebounce: ReturnType<typeof createDebouncer>;
  private completionsDebounce: ReturnType<typeof createDebouncer>;
  private validation: QueryBarValidation = emptyQueryBarValidation();
  private completions: QueryCompletion[] = [];
  private activeIndex = -1;
  private listOpen = false;
  private published = readExprParam();
  private composing = false;
  private generation = 0;

  constructor(private els: QueryBarElements, deps: QueryBarDeps) {
    const fetcher = deps.fetch || ((input, init) => fetch(input, init));
    const scheduler = deps.scheduler || windowScheduler(window);
    this.requester = createQueryRequester(fetcher);
    this.validateDebounce = createDebouncer(scheduler);
    this.completionsDebounce = createDebouncer(scheduler);
    this.bindEvents();
    const initial = readExprParam();
    if (initial !== "") {
      els.input.value = initial;
      this.validation = reduceQueryBar(this.validation, { type: "input", text: initial });
      this.runValidate(initial);
    } else {
      this.renderAll();
    }
  }

  private renderList(): void {
    if (!this.listOpen) {
      closeQueryBarCompletions(this.els);
      return;
    }
    renderQueryBarCompletions(this.els, this.completions, {
      activeIndex: this.activeIndex,
      onChoose: (completion) => {
        this.acceptCompletion(completion);
      },
    });
  }

  private closeList(): void {
    this.listOpen = false;
    this.completions = [];
    this.activeIndex = -1;
    this.completionsDebounce.cancel();
    this.renderList();
  }

  private renderAll(): void {
    renderQueryBarStatus(this.els, this.validation);
    renderQueryBarError(this.els, this.validation);
  }

  /**
   * Publishes a confirmed expression: the URL gains `expr`, and the dispatch is
   * what makes every listener on the page — the Library results, the graph —
   * refetch through `buildFilteredAPIURL`, which carries the parameter.
   *
   * `mode: ""` reaches every listener regardless of its facet mode, because the
   * expression is the page's outermost filter rather than one panel's state.
   */
  private publish(expr: string): void {
    if (expr === this.published) {
      return;
    }
    this.published = expr;
    syncExprParam(expr);
    dispatchFiltersChange(window, {
      mode: "",
      params: new URLSearchParams(window.location.search),
    });
  }

  private runValidate(text: string): void {
    if (this.composing) return;
    const generation = this.generation;
    this.validation = reduceQueryBar(this.validation, { type: "request", text });
    this.renderAll();
    void this.requester
      .validate(text)
      .then((response) => {
        if (response === null || generation !== this.generation) {
          return;
        }
        this.validation = reduceQueryBar(this.validation, { type: "response", text, response });
        this.renderAll();
        const confirmed = publishableQuery(this.validation);
        if (confirmed !== null) {
          this.publish(confirmed);
        }
      })
      .catch(() => {
        if (generation !== this.generation) return;
        this.validation = reduceQueryBar(this.validation, { type: "failure", text });
        this.renderAll();
      });
  }

  private applyCompletionsResponse(
    context: QueryCompletionContext,
    incoming: QueryCompletion[]
  ): void {
    // The reply describes the text as it was when the request went out. Both
    // the fresh list and the rows already on screen are narrowed to the token
    // under the caret *now*, so keystrokes that landed in flight narrow the
    // popup instead of resurrecting rows. Merging the two — fresh first — is
    // what keeps the tail populated when the server's 50-row cap trims a
    // broader prefix differently than a narrower one.
    this.completions = mergeCompletions([
      filterCompletions(incoming, context.prefix),
      filterCompletions(this.completions, context.prefix),
    ]);
    this.activeIndex = this.completions.length > 0 ? 0 : -1;
    this.listOpen = this.completions.length > 0;
    this.renderList();
  }

  private runCompletions(text: string, pos: number): void {
    const generation = this.generation;
    void this.requester
      .completions(text, pos)
      .then((response) => {
        if (response === null || generation !== this.generation) {
          return;
        }
        const context = completionContextAt(this.els.input.value, this.caretPosition());
        this.applyCompletionsResponse(context, response.completions);
      })
      .catch(() => {
        if (generation !== this.generation) return;
        this.closeList();
      });
  }

  private caretPosition(): number {
    const selection = this.els.input.selectionStart;
    return typeof selection === "number" ? selection : this.els.input.value.length;
  }

  private acceptCompletion(completion: QueryCompletion): void {
    if (this.composing) return;
    const text = this.els.input.value;
    const context = completionContextAt(text, this.caretPosition());
    const edit = applyCompletion(text, context.replace, completion.insert);
    this.els.input.value = edit.text;
    this.els.input.setSelectionRange(edit.pos, edit.pos);
    this.els.input.focus();
    this.closeList();
    // Accepting a field name leaves the caret just past its ":", where the
    // value list is the obvious next thing to show. Accepting a value finishes
    // the token, so the popup stays shut and only the validation runs.
    this.handleTextChange(edit.text, {
      immediate: true,
      completionCause: context.kind === "field" ? "manual" : null,
    });
  }

  private requestCompletions(text: string, cause: CompletionCause): void {
    if (this.composing) return;
    const pos = this.caretPosition();
    if (!shouldRequestCompletions(text, pos, cause)) {
      this.closeList();
      return;
    }
    // The rows already on screen narrow immediately, so the popup tracks the
    // keystroke rather than the round trip.
    if (this.listOpen) {
      const context = completionContextAt(text, pos);
      this.completions = filterCompletions(this.completions, context.prefix);
      this.activeIndex = this.completions.length > 0 ? Math.min(this.activeIndex, this.completions.length - 1) : -1;
      this.renderList();
    }
    this.completionsDebounce.run(() => {
      this.runCompletions(this.els.input.value, this.caretPosition());
    }, cause === "manual" ? 0 : completionsDebounceMs);
  }

  private handleTextChange(
    text: string,
    { immediate = false, completionCause = "input" }: TextChangeOptions = {}
  ): void {
    if (this.composing) return;
    this.validation = reduceQueryBar(this.validation, { type: "input", text });
    this.renderAll();

    if (isBlankQuery(text)) {
      this.validateDebounce.cancel();
      this.requester.cancel();
      this.publish("");
      if (completionCause === "focus" || completionCause === "manual") {
        this.requestCompletions(text, completionCause);
      } else {
        this.closeList();
      }
      return;
    }

    this.validateDebounce.run(() => {
      this.runValidate(this.els.input.value);
    }, immediate ? 0 : validateDebounceMs);
    if (completionCause !== null) {
      this.requestCompletions(text, completionCause);
    }
  }

  private bindEvents(): void {
    const { input, clearButton, retryButton } = this.els;
    input.addEventListener("input", (event) => {
      if (event.isComposing) {
        if (!this.composing) this.startComposition();
        return;
      }
      this.handleTextChange(input.value);
    });
    input.addEventListener("compositionstart", () => this.startComposition());
    input.addEventListener("compositionend", () => {
      this.composing = false;
      this.handleTextChange(input.value);
    });
    input.addEventListener("focus", () => {
      if (input.value === "") this.requestCompletions("", "focus");
    });
    input.addEventListener("blur", () => this.closeList());
    input.addEventListener("keydown", (event) => this.onKeyDown(event));
    clearButton?.addEventListener("click", () => this.clear());
    retryButton?.addEventListener("click", () => {
      if (this.composing || this.validation.phase !== "unavailable") return;
      this.validateDebounce.cancel();
      this.runValidate(input.value);
      input.focus();
    });
  }

  private startComposition(): void {
    this.composing = true;
    this.generation += 1;
    this.validateDebounce.cancel();
    this.requester.cancel();
    this.closeList();
    // Clear old diagnostics without publishing intermediate composition text.
    this.validation = emptyQueryBarValidation();
    this.renderAll();
  }

  private onKeyDown(event: KeyboardEvent): void {
    if (this.composing || isCompositionKey(event)) return;
    if (event.key === " " && event.ctrlKey) {
      event.preventDefault();
      this.requestCompletions(this.els.input.value, "manual");
      return;
    }
    if (event.key === "Escape") {
      if (this.listOpen) {
        event.preventDefault();
        this.closeList();
      }
      return;
    }
    if (!this.listOpen || this.completions.length === 0) {
      if (event.key === "Enter") {
        event.preventDefault();
        this.validateDebounce.run(() => this.runValidate(this.els.input.value), 0);
      }
      return;
    }
    this.onCompletionKey(event);
  }

  private onCompletionKey(event: KeyboardEvent): void {
    switch (event.key) {
      case "ArrowDown":
      case "ArrowUp":
        event.preventDefault();
        this.activeIndex = nextActiveIndex(this.activeIndex, event.key === "ArrowDown" ? 1 : -1, this.completions.length);
        this.renderList();
        return;
      case "Enter":
      case "Tab": {
        const completion = this.completions[this.activeIndex];
        if (!completion) return;
        event.preventDefault();
        this.acceptCompletion(completion);
        return;
      }
      default:
    }
  }

  private clear(): void {
    this.els.input.value = "";
    this.generation += 1;
    this.validateDebounce.cancel();
    this.requester.cancel();
    this.validation = reduceQueryBar(this.validation, { type: "reset" });
    this.renderAll();
    this.closeList();
    this.publish("");
    this.els.input.focus();
  }
}

export const initQueryBarController = (els: QueryBarElements, deps: QueryBarDeps = {}): void => {
  new QueryBarController(els, deps);
};
