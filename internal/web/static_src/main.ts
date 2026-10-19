import { initBrokenCreateActions } from "./broken-create";
import { initCapture } from "./capture";
import { initCopyControls } from "./clipboard";
import { initWorkspacePopovers } from "./disclosure-popover";
import { initOpenInEditor } from "./editor-open";
import { initFolderSuggestions } from "./folder-suggestions";
import { initFrontmatterEditing } from "./frontmatter-edit";
import { initHistoryPanel } from "./history-panel";
import { initNoteReading } from "./note-reading";
import { initNoteNavigation } from "./note-navigation";
import { initNoteMobileSidebars } from "./note-mobile-sidebars";
import { initNoteLayout } from "./note-layout";
import { initNoteInsight } from "./note-insight";
import { initNotePinAction } from "./note-pin";
import { initTrashNoteAction } from "./note-trash";
import { initTrashUndo } from "./note-trash-undo";
import { initPinnedList } from "./pinned-list";
import { initRelatedNotes } from "./related";
import { initSavedViews } from "./saved-views";
import { initSearch } from "./search";
import { initNewNote } from "./new-note";
import { initSyncLine } from "./sync-line";
import { initTheme } from "./theme";
import { initPendingWriteOutcomes } from "./write-outcome-check";

(function () {
  const readOnly = document.documentElement.dataset.readOnly === "true";
  initTheme();
  initWorkspacePopovers();
  if (!readOnly) {
    initPendingWriteOutcomes();
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
  }
  // The synchronization line is on every page in every mode: a replica that
  // publishes a copy has a status worth reading too, and the route answers it.
  initSyncLine();
  initNoteLayout();
  initNoteMobileSidebars();
  initNoteNavigation();
  initNoteReading();
  initCopyControls();
  initRelatedNotes();
  initSearch();
  initSavedViews();
})();
