import { initBrokenCreateActions } from "./broken-create";
import { initCopyControls } from "./clipboard";
import { initOpenInEditor } from "./editor-open";
import { initFacets } from "./facets";
import { initFrontmatterEditing } from "./frontmatter-edit";
import { initGraph } from "./graph-controller";
import { initHistoryPanel } from "./history-panel";
import { initTrashNoteAction } from "./note-trash";
import { initOverviewSearchResults } from "./overview-search";
import { initSearch } from "./search";

(function () {
  initFacets();
  initOpenInEditor();
  initTrashNoteAction();
  initFrontmatterEditing();
  initBrokenCreateActions();
  initCopyControls();
  initGraph();
  initSearch();
  initOverviewSearchResults();
  initHistoryPanel();
})();
