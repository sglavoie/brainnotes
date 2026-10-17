// Loaded only by this workspace; shared reading does not initialize it.
import { initHistoryWorkspace } from "./history-workspace";

if (document.documentElement.dataset.readOnly !== "true") {
  initHistoryWorkspace();
}
