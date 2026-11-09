import { initHistoryPanel } from "./history-panel";
// Loaded only by this workspace; shared reading does not initialize it.
import { initProperties } from "./properties";

initProperties();

if (document.documentElement.dataset.readOnly !== "true") initHistoryPanel();
