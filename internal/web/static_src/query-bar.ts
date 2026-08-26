import {
  initQueryBarController,
  resolveQueryBarElements,
  type QueryBarDeps,
} from "./query-bar-controller";

export type { QueryBarDeps } from "./query-bar-controller";

/**
 * Hydrates the Library query bar. The markup is rendered by library.html and
 * this is the only symbol main.ts consumes; everything else in the query-bar-*
 * cluster is reachable from the tests and from here.
 */
export const initQueryBar = (deps: QueryBarDeps = {}): void => {
  const els = resolveQueryBarElements(document);
  if (!els) {
    return;
  }
  initQueryBarController(els, deps);
};
