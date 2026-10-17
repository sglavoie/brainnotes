// Loaded only by this workspace; shared reading does not initialize it.
import { initGraph } from "./graph-controller";
import { initFacets } from "./facets";
import { initQueryBar } from "./query-bar";

initGraph();
initFacets();
initQueryBar();
