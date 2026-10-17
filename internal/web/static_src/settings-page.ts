// Loaded only by this workspace; shared reading does not initialize it.
import { initSettings } from "./settings";

if (document.documentElement.dataset.readOnly !== "true") {
  initSettings();
}
