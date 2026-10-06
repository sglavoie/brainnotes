import type { QueryCompletion } from "./api";
import { setStatusText } from "./status";
import { completionLabel } from "./query-bar-completions";
import { errorCaretRow, queryBarStatus, type QueryBarValidation } from "./query-bar-model";

/** The DOM the query bar renders into. Every node but the input is optional. */
export type QueryBarElements = {
  root: HTMLElement;
  input: HTMLInputElement;
  statusEl: HTMLElement | null;
  errorEl: HTMLElement | null;
  errorMessageEl: HTMLElement | null;
  errorQueryEl: HTMLElement | null;
  errorCaretEl: HTMLElement | null;
  suggestionsEl: HTMLElement | null;
  listEl: HTMLElement | null;
  clearButton: HTMLButtonElement | null;
  retryButton: HTMLButtonElement | null;
};

export const completionOptionID = (index: number): string => `query-bar-option-${index}`;

export const renderQueryBarStatus = (
  els: QueryBarElements,
  state: QueryBarValidation
): void => {
  if (els.retryButton) els.retryButton.hidden = state.phase !== "unavailable";
  const status = queryBarStatus(state);
  setStatusText(els.statusEl, status.message, status.kind);
  els.root.setAttribute("data-query-bar-phase", state.phase);
  els.input.setAttribute("aria-invalid", state.phase === "invalid" ? "true" : "false");
};

/**
 * Renders the error strip: the message, the query echoed verbatim, and a caret
 * row under it. The echo and the caret are two rows of the same monospace block
 * so column N of one sits over column N of the other — which is why the caret
 * is drawn rather than the input being highlighted, and why the echo cannot be
 * trimmed or elided.
 */
export const renderQueryBarError = (
  els: QueryBarElements,
  state: QueryBarValidation
): void => {
  const { errorEl } = els;
  if (!errorEl) {
    return;
  }
  const error = state.phase === "invalid" ? state.error : null;
  if (!error) {
    clearQueryBarError(els);
    return;
  }

  errorEl.hidden = false;
  if (els.errorMessageEl) {
    els.errorMessageEl.textContent = error.positioned
      ? `Column ${error.column}: ${error.message}`
      : error.message;
  }
  const caret = errorCaretRow(state.query, error);
  if (els.errorQueryEl) {
    els.errorQueryEl.textContent = caret === "" ? "" : state.query;
  }
  if (els.errorCaretEl) {
    els.errorCaretEl.textContent = caret;
  }
  renderQueryBarSuggestions(els.suggestionsEl, error.suggestions);
};

const renderQueryBarSuggestions = (element: HTMLElement | null, suggestions: string[]): void => {
  if (!element) return;
  element.hidden = suggestions.length === 0;
  element.textContent = suggestions.length === 0 ? "" : `Did you mean: ${suggestions.join(", ")}?`;
};

const clearQueryBarError = (els: QueryBarElements): void => {
  if (els.errorEl) els.errorEl.hidden = true;
  for (const element of [els.errorMessageEl, els.errorQueryEl, els.errorCaretEl, els.suggestionsEl]) {
    if (element) element.textContent = "";
  }
  if (els.suggestionsEl) els.suggestionsEl.hidden = true;
};

export type CompletionRenderOptions = {
  activeIndex: number;
  onChoose: (completion: QueryCompletion, index: number) => void;
};

/**
 * Renders the candidate list as a `role="listbox"` of `role="option"` rows and
 * points `aria-activedescendant` at the active one.
 *
 * The rows are plain `<li>` elements, not buttons: focus stays in the input the
 * whole time — that is what a combobox is — so a focusable row would be a
 * second tab stop that keyboard navigation never visits.
 */
export const renderQueryBarCompletions = (
  els: QueryBarElements,
  completions: QueryCompletion[],
  options: CompletionRenderOptions
): void => {
  const { listEl, input } = els;
  if (!listEl) {
    return;
  }
  const doc = listEl.ownerDocument;
  listEl.textContent = "";

  if (completions.length === 0) {
    listEl.hidden = true;
    input.setAttribute("aria-expanded", "false");
    input.removeAttribute("aria-activedescendant");
    return;
  }

  completions.forEach((completion, index) => {
    const item = doc.createElement("li");
    item.className = "query-bar-completion";
    item.id = completionOptionID(index);
    item.setAttribute("role", "option");
    item.setAttribute("aria-selected", index === options.activeIndex ? "true" : "false");
    item.setAttribute("data-query-bar-completion", String(index));
    if (index === options.activeIndex) {
      item.classList.add("is-active");
    }

    const value = doc.createElement("span");
    value.className = "query-bar-completion-value";
    value.textContent = completion.value;
    item.appendChild(value);

    if (completion.kind) {
      const kind = doc.createElement("span");
      kind.className = "query-bar-completion-kind";
      kind.textContent = completion.kind;
      item.appendChild(kind);
    }

    const detail = completion.detail.trim();
    if (detail !== "" || completion.count > 0) {
      const meta = doc.createElement("span");
      meta.className = "query-bar-completion-detail";
      meta.textContent = detail !== "" ? detail : `${completion.count} notes`;
      item.appendChild(meta);
    }

    item.setAttribute("aria-label", completionLabel(completion));
    // mousedown, not click: click fires after the input has already lost focus
    // and the blur handler has closed the list out from under the pointer.
    item.addEventListener("mousedown", (event) => {
      event.preventDefault();
      options.onChoose(completion, index);
    });

    listEl.appendChild(item);
  });

  listEl.hidden = false;
  input.setAttribute("aria-expanded", "true");
  if (options.activeIndex >= 0 && options.activeIndex < completions.length) {
    input.setAttribute("aria-activedescendant", completionOptionID(options.activeIndex));
  } else {
    input.removeAttribute("aria-activedescendant");
  }
};

export const closeQueryBarCompletions = (els: QueryBarElements): void => {
  renderQueryBarCompletions(els, [], { activeIndex: -1, onChoose: () => {} });
};
