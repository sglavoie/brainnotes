// Loaded only by this workspace; shared reading does not initialize it.
import { initGraph } from "./graph-controller";
import { initFacets } from "./facets";
import { initQueryBar } from "./query-bar";
import { browserQueryChangeEventName, hasActiveFilters } from "./filters";
import { readPageDisplayParams } from "./graph-controller-params";

const initRefineDisclosure = (): void => {
  const disclosure = document.querySelector<HTMLDetailsElement>(".graph-refine-options");
  const summary = disclosure?.querySelector("summary");
  if (!disclosure || !summary) return;
  const refresh = (): boolean => {
    const display = readPageDisplayParams();
    const active = hasActiveFilters() || Boolean(display.isolateID || display.minDegree || display.collapseDepth);
    summary.textContent = active ? "Refine graph · active" : "Refine graph";
    return active;
  };
  disclosure.open = refresh();
  window.addEventListener(browserQueryChangeEventName, refresh);
};

initRefineDisclosure();
initGraph();
initFacets();
initQueryBar();
