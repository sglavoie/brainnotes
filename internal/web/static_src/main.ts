import { initBrokenCreateActions } from "./broken-create";
import { initCopyControls } from "./clipboard";
import { initDailyFixes } from "./daily-fixes";
import { initOpenInEditor } from "./editor-open";
import { initFacets } from "./facets";
import { initFrontmatterEditing } from "./frontmatter-edit";
import { initGraph } from "./graph-controller";
import { initHistoryPanel } from "./history-panel";
import { initNoteLayout } from "./note-layout";
import { initTrashNoteAction } from "./note-trash";
import { initOverviewSearchResults } from "./overview-search";
import { initSearch } from "./search";
import { initTheme } from "./theme";

(function () {
  initTheme();
  initFacets();
  initOpenInEditor();
  initTrashNoteAction();
  initNoteLayout();
  initFrontmatterEditing();
  initBrokenCreateActions();
  initCopyControls();
  initGraph();
  initSearch();
  initOverviewSearchResults();
  initHistoryPanel();
  initDailyFixes();
})();
