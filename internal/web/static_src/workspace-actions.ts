import { initFolderSuggestions } from "./folder-suggestions";
// Creation, pinned-list management and broken-link repairs are loaded only
// by their workspaces; reading pages do not pay for these mutation forms.
import { initNewNote } from "./new-note";
import { initPinnedList } from "./pinned-list";
import { initBrokenCreateActions } from "./broken-create";

if (document.documentElement.dataset.readOnly !== "true") {
  initNewNote();
  initPinnedList();
  initBrokenCreateActions();
}

if (document.documentElement.dataset.readOnly !== "true") initFolderSuggestions();
