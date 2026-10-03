import { queryElement } from "./dom";
import { filterChangeEventName, normalizeParamValue, sharedFilterParamKeys } from "./filters";
import { savedViewInputFromParams } from "./saved-views-model";

// Describe the same URL shape the save action reads; never imply that the
// independent text search and facet chips are part of the stored expression.
export const bindSavedViewScope = (form: HTMLFormElement): (() => void) => {
  const scope = queryElement(form, "[data-saved-views-scope]", HTMLElement);
  const warning = queryElement(form, "[data-saved-views-excluded]", HTMLElement);
  const refresh = (): void => {
    const params = new URLSearchParams(window.location.search);
    const input = savedViewInputFromParams(params, { name: "", description: "" });
    if (scope) {
      scope.textContent = [
        "This view will save:",
        `Query: ${input.query || "(write a query expression first)"}`,
        `Sort: ${input.sort || "default"}`,
        ...(input.relativeTo ? [`Relative to: ${input.relativeTo}`] : []),
        `Limit: ${input.limit || "none"}`,
      ].join("\n");
    }
    if (warning) {
      const excluded = ["q", ...sharedFilterParamKeys].flatMap((key) =>
        params.getAll(key).map(normalizeParamValue).filter(Boolean).map((value) =>
          `${key === "q" ? "Text search" : key}: ${value}`));
      warning.hidden = excluded.length === 0;
      warning.textContent = excluded.length
        ? `Not saved — these filters only affect the current results: ${excluded.join("; ")}.`
        : "";
    }
  };
  window.addEventListener(filterChangeEventName, refresh);
  form.ownerDocument.addEventListener("input", (event) => {
    if (event.target instanceof HTMLElement && event.target.id === "search-input") {
      refresh();
    }
  });
  return refresh;
};
