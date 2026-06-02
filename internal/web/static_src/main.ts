import { initCopyControls } from "./clipboard";
import { initOpenInEditor } from "./editor-open";
import { initFacets } from "./facets";
import { initGraph } from "./graph-controller";
import { initOverviewSearchResults } from "./overview-search";
import { initSearch } from "./search";

(function () {
  initFacets();
  initOpenInEditor();
  initCopyControls();
  initGraph();
  initSearch();
  initOverviewSearchResults();
})();
