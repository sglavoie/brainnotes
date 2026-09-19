import { initBrokenCreateActions } from "./broken-create";
import { initCapture } from "./capture";
import { initCopyControls } from "./clipboard";
import { initDailyBrief } from "./daily-brief";
import { initDailyFixes } from "./daily-fixes";
import { initOpenInEditor } from "./editor-open";
import { initFacets } from "./facets";
import { initFrontmatterEditing } from "./frontmatter-edit";
import { initGraph } from "./graph-controller";
import { initHistoryPanel } from "./history-panel";
import { initHistoryWorkspace } from "./history-workspace";
import { initNoteLayout } from "./note-layout";
import { initNoteInsight } from "./note-insight";
import { initNotePinAction } from "./note-pin";
import { initTrashNoteAction } from "./note-trash";
import { initOverviewSearchResults } from "./overview-search";
import { initPinnedList } from "./pinned-list";
import { initProperties } from "./properties";
import { initQueryBar } from "./query-bar";
import { initRelatedNotes } from "./related";
import { initSavedViews } from "./saved-views";
import { initSearch } from "./search";
import { initSettings } from "./settings";
import { initSourceEditor } from "./source-editor";
import { initSync } from "./sync";
import { initSyncLine } from "./sync-line";
import { initSynthesize } from "./synthesize";
import { initTheme } from "./theme";

(function () {
  const readOnly = document.documentElement.dataset.readOnly === "true";
  initTheme();
  if (!readOnly) {
    initCapture();
    initOpenInEditor();
    initNotePinAction();
    initTrashNoteAction();
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
    // The /edit page is refused outright in read-only mode, so the editor is
    // built only where a save can be accepted.
    initSourceEditor();
    // The /sync page resolves conflicts, which is a write, and is refused in
    // read-only mode for the same reason.
    initSync();
  }
  // The synchronization line is on every page in every mode: a replica that
  // publishes a copy has a status worth reading too, and the route answers it.
  initSyncLine();
  initFacets();
  initNoteLayout();
  initCopyControls();
  initDailyBrief();
  initRelatedNotes();
  initGraph();
  initSearch();
  initQueryBar();
  initSavedViews();
  initOverviewSearchResults();
  initDailyFixes();
  initProperties();
})();
