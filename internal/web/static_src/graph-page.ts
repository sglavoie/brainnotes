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
  const layoutActive = (): boolean => {
    const display = readPageDisplayParams();
    return Boolean(display.isolateID || display.minDegree || display.collapseDepth);
  };
  const refresh = (): boolean => {
    const active = hasActiveFilters() || layoutActive();
    summary.textContent = active ? "Refine graph · active" : "Refine graph";
    return active;
  };
  disclosure.open = refresh();
  // The layout settings live in a nested disclosure: when one of them is what
  // makes the graph "active", open it too so the setting is visible.
  const layout = disclosure.querySelector<HTMLDetailsElement>(".graph-layout-options");
  if (layout && layoutActive()) layout.open = true;
  window.addEventListener(browserQueryChangeEventName, refresh);
};

initRefineDisclosure();
initGraph();
initFacets();
initQueryBar();
