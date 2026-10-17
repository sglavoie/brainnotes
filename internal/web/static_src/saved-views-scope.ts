import { queryElement } from "./dom";
import { filterChangeEventName, sharedFilterParamKeys } from "./filters";
import { savedViewInputFromParams } from "./saved-views-model";

// Describe the complete selection the save action freezes.
export const bindSavedViewScope = (form: HTMLFormElement): (() => void) => {
  const scope = queryElement(form, "[data-saved-views-scope]", HTMLElement);
  const warning = queryElement(form, "[data-saved-views-excluded]", HTMLElement);
  const refresh = (): void => {
    const params = new URLSearchParams(window.location.search);
    const input = savedViewInputFromParams(params, { name: "", description: "" });
    if (scope) {
      scope.textContent = [
        "This view will save:",
        `Query: ${input.query || "all matching notes"}`,
        ...["q", ...sharedFilterParamKeys].flatMap((key) => params.getAll(key).map((value) => `${key === "q" ? "Text search" : key}: ${value}`)),
        `Sort: ${input.sort || "best match"}`,
        ...(input.relativeTo ? [`Relative to: ${input.relativeTo}`] : []),
        `Limit: ${input.limit || "none"}`,
      ].join("\n");
    }
    if (warning) {
      warning.hidden = true;
      warning.textContent = "";
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
