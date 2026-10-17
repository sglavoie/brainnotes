// Loaded only by the editor and Sync pages; ordinary reading needs no CodeMirror.
import { initSourceEditor } from "./source-editor";
import { initSync } from "./sync";

if (document.documentElement.dataset.readOnly !== "true") {
  initSourceEditor();
  initSync();
}
