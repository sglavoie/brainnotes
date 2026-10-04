import { queryAllElements, queryElement } from "./dom";
import { FacetController } from "./facet-controller";
export * from "./facet-model";

export const initFacets = (): void => {
  for (const root of queryAllElements(document, "[data-facet-controls]", HTMLElement)) {
    const input = queryElement(root, ".facet-input", HTMLInputElement);
    const options = queryElement(root, ".facet-options", HTMLElement);
    const chips = queryElement(root, "[data-facet-chips]", HTMLElement);
    if (input && options && chips) new FacetController(root, input, options, chips).init();
  }
};
