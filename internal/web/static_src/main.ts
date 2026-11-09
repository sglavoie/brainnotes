import { initCommandSwitcher } from "./command-switcher";
import { initSideReference } from "./side-reference";
import { initCapture } from "./capture";
import { initGlobalCapture } from "./global-capture";
import { initCopyControls } from "./clipboard";
import { initWorkspacePopovers } from "./disclosure-popover";
import { initOpenInEditor } from "./editor-open";
import { initFrontmatterEditing } from "./frontmatter-edit";
import { initNoteReading } from "./note-reading";
import { initReadingComfort } from "./reading-comfort";
import { initNotePeek } from "./note-peek";
import { initNoteNavigation } from "./note-navigation";
import { initNoteMobileSidebars } from "./note-mobile-sidebars";
import { initNoteLayout } from "./note-layout";
import { initNoteInsight } from "./note-insight";
import { initNotePinAction } from "./note-pin";
import { initTrashNoteAction } from "./note-trash";
import { initTrashUndo } from "./note-trash-undo";
import { initRelatedNotes } from "./related";
import { initSavedViews } from "./saved-views";
import { initSearch } from "./search";
import { initSyncLine } from "./sync-line";
import { initTheme } from "./theme";
import { initPendingWriteOutcomes } from "./write-outcome-check";

(function () {
  const readOnly = document.documentElement.dataset.readOnly === "true";
  initTheme();
  initWorkspacePopovers();
  initCommandSwitcher();
  if (!readOnly) {
    initPendingWriteOutcomes();
    initGlobalCapture();
    initCapture();
    initOpenInEditor();
    initNotePinAction();
    initTrashNoteAction();
    initTrashUndo();
    initNoteInsight();
    initFrontmatterEditing();
  }
  // The synchronization line is on every page in every mode: a replica that
  // publishes a copy has a status worth reading too, and the route answers it.
  initSyncLine();
  initNoteLayout();
  initNoteMobileSidebars();
  initNoteNavigation();
  initNoteReading();
  initReadingComfort();
  initNotePeek();
  initSideReference();
  initCopyControls();
  initRelatedNotes();
  initSearch();
  initSavedViews();
})();
