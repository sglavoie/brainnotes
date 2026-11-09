import { initHistoryPanel } from "./history-panel";
// Loaded only by this workspace; shared reading does not initialize it.
import { initSettings } from "./settings";

if (document.documentElement.dataset.readOnly !== "true") {
  initSettings();
}

if (document.documentElement.dataset.readOnly !== "true") initHistoryPanel();
