import { synthesisSourcePosition } from "./synthesize-model";
import {
  addSynthesisSource,
  moveSynthesisSource,
  removeSynthesisSource,
  reorderSynthesisSources,
} from "./synthesize-sources";
import type { SynthesisRuntime } from "./synthesize-runtime";

export type SynthesisSelectionActions = {
  addSource: (key: string) => void;
  removeSource: (key: string) => void;
  moveSource: (key: string, direction: -1 | 1) => void;
  startDrag: (row: HTMLElement, event: PointerEvent) => void;
};

export const createSynthesisSelectionActions = (
  runtime: SynthesisRuntime
): SynthesisSelectionActions => {
  const { config, model, view } = runtime;

  const buttonByAttribute = (attribute: string, value: string): HTMLButtonElement | null => {
    for (const button of view.sourceList.querySelectorAll<HTMLButtonElement>(`[${attribute}]`)) {
      if (button.getAttribute(attribute) === value) {
        return button;
      }
    }
    return null;
  };

  const focusHandle = (key: string): void => {
    (buttonByAttribute("data-synthesize-handle", key) || view.selectionStatus).focus({
      preventScroll: true,
    });
  };

  // A removed row takes its own controls with it, so focus lands on whatever
  // now occupies that position, then the row before it, then the selection
  // summary — never nothing.
  const focusRemoveAt = (index: number): void => {
    const source = model.sources[index] || model.sources[index - 1];
    const button = source ? buttonByAttribute("data-synthesize-remove", source.key) : null;
    (button || view.selectionStatus).focus({ preventScroll: true });
  };

  const addSource = (key: string): void => {
    const candidate = runtime.candidates.get(key);
    if (!candidate) {
      runtime.announce("That note is no longer available to add.", "is-error");
      return;
    }
    const outcome = addSynthesisSource(model, config, candidate);
    runtime.announce(outcome.message, outcome.ok ? "is-success" : "is-error");
    if (!outcome.ok) {
      return;
    }
    runtime.render();
    runtime.renderCandidatePanels();
    runtime.persist();
  };

  const removeSource = (key: string): void => {
    const index = model.sources.findIndex((source) => source.key === key);
    const outcome = removeSynthesisSource(model, key);
    runtime.announce(outcome.message, outcome.ok ? "is-success" : "is-error");
    if (!outcome.ok) {
      return;
    }
    runtime.render();
    runtime.renderCandidatePanels();
    runtime.persist();
    focusRemoveAt(index);
  };

  const moveSource = (key: string, direction: -1 | 1): void => {
    const outcome = moveSynthesisSource(model, key, direction);
    runtime.announce(outcome.message, outcome.ok ? "is-success" : "is-error");
    if (!outcome.ok) {
      return;
    }
    runtime.render();
    runtime.persist();
    focusHandle(key);
  };

  const applyDragOrder = (key: string): void => {
    const keys = Array.from(view.sourceList.querySelectorAll<HTMLElement>("[data-synthesize-source]"))
      .filter((row) => !row.hasAttribute("data-synthesize-anchor"))
      .map((row) => row.getAttribute("data-synthesize-source") || "");
    const moved = model.sources.find((source) => source.key === key);
    const outcome = reorderSynthesisSources(model, keys);
    runtime.render();
    if (outcome.ok) {
      runtime.persist();
      runtime.announce(
        `Moved ${moved?.title || key} to position ${synthesisSourcePosition(model, key)} of ${model.sources.length + 1}.`,
        "is-success"
      );
    }
    focusHandle(key);
  };

  const supporterRows = (): HTMLElement[] =>
    Array.from(view.sourceList.querySelectorAll<HTMLElement>("[data-synthesize-source]")).filter(
      (row) => !row.hasAttribute("data-synthesize-anchor")
    );

  const startDrag = (row: HTMLElement, event: PointerEvent): void => {
    if (event.button !== 0) {
      return;
    }
    event.preventDefault();
    const key = row.getAttribute("data-synthesize-source") || "";
    const win = view.sourceList.ownerDocument.defaultView;
    if (!win) {
      return;
    }
    row.classList.add("is-dragging");

    const onPointerMove = (moveEvent: PointerEvent): void => {
      const next = supporterRows().find((other) => {
        if (other === row) {
          return false;
        }
        const rect = other.getBoundingClientRect();
        return moveEvent.clientY < rect.top + rect.height / 2;
      });
      if (next) {
        view.sourceList.insertBefore(row, next);
      } else {
        view.sourceList.appendChild(row);
      }
    };

    const finishDrag = (): void => {
      row.classList.remove("is-dragging");
      win.removeEventListener("pointermove", onPointerMove);
      win.removeEventListener("pointerup", finishDrag);
      win.removeEventListener("pointercancel", finishDrag);
      applyDragOrder(key);
    };

    win.addEventListener("pointermove", onPointerMove);
    win.addEventListener("pointerup", finishDrag, { once: true });
    win.addEventListener("pointercancel", finishDrag, { once: true });
  };

  return { addSource, removeSource, moveSource, startDrag };
};
