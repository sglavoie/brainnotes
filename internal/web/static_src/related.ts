import { createRelatedController, type RelatedControllerDeps } from "./related-controller";
import { queryElement } from "./dom";

export { buildRelatedRequestURL, normalizeRelatedResponse } from "./related-api";
export { readRelatedConfig } from "./related-controller";
export type { RelatedControllerDeps } from "./related-controller";
export type * from "./related-types";

export const initRelatedNotes = (deps: RelatedControllerDeps = {}): void => {
  const root = queryElement(document, "[data-related-notes]", HTMLElement);
  if (!root) {
    return;
  }
  createRelatedController(root, deps);
};
