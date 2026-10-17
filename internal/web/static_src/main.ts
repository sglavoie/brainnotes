import { initLibrarySearch } from "./library-search";
import { initBrokenCreateActions } from "./broken-create";
import { initCapture } from "./capture";
import { initCopyControls } from "./clipboard";
import { initDailyBrief } from "./daily-brief";
import { initDailyFixes } from "./daily-fixes";
import { initOpenInEditor } from "./editor-open";
import { initFacets } from "./facets";
import { initFolderSuggestions } from "./folder-suggestions";
import { initFrontmatterEditing } from "./frontmatter-edit";
import { initGraph } from "./graph-controller";
import { initHistoryPanel } from "./history-panel";
import { initHistoryWorkspace } from "./history-workspace";
import { initLibrarySort } from "./library-sort";
import { initLibrarySearchLink } from "./library-search-link";
import { initNoteReading } from "./note-reading";
import { initNoteNavigation } from "./note-navigation";
import { initNoteMobileSidebars } from "./note-mobile-sidebars";
import { initNoteLayout } from "./note-layout";
import { initNoteInsight } from "./note-insight";
import { initNotePinAction } from "./note-pin";
import { initTrashNoteAction } from "./note-trash";
import { initTrashUndo } from "./note-trash-undo";
import { initOverviewSearchResults } from "./overview-search";
import { initPinnedList } from "./pinned-list";
import { initProperties } from "./properties";
import { initQueryBar } from "./query-bar";
import { initRelatedNotes } from "./related";
import { initSavedViews } from "./saved-views";
import { initSearch } from "./search";
import { initSettings } from "./settings";
import { initNewNote } from "./new-note";
import { initSyncLine } from "./sync-line";
import { initSynthesize } from "./synthesize";
import { initTheme } from "./theme";

(function () {
  const readOnly = document.documentElement.dataset.readOnly === "true";
  initTheme();
  if (!readOnly) {
    initFolderSuggestions();
    initCapture();
    initNewNote();
    initOpenInEditor();
    initNotePinAction();
    initTrashNoteAction();
    initTrashUndo();
    initPinnedList();
    initNoteInsight();
    initFrontmatterEditing();
    initBrokenCreateActions();
    initHistoryPanel();
    // The /history page restores earlier content, which is a write, and a
    // read-only replica does not serve it at all — the route refuses and the
    // nav link is absent, so the workspace is built only where it exists.
    initHistoryWorkspace();
    initSynthesize();
    // The /settings page writes brainnotes.toml and the templates directory, so
    // like the editor it is built only where a write can be accepted.
    initSettings();
  }
  // The synchronization line is on every page in every mode: a replica that
  // publishes a copy has a status worth reading too, and the route answers it.
  initSyncLine();
  initFacets();
  initNoteLayout();
  initNoteMobileSidebars();
  initNoteNavigation();
  initNoteReading();
  initLibrarySearchLink();
  initLibrarySearch();
  initCopyControls();
  initDailyBrief();
  initRelatedNotes();
  initGraph();
  initSearch();
  initQueryBar();
  initSavedViews();
  initLibrarySort();
  initOverviewSearchResults();
  initDailyFixes();
  initProperties();
})();
