// Loaded only by this workspace; shared reading does not initialize it.
import { initSynthesize } from "./synthesize";

if (document.documentElement.dataset.readOnly !== "true") {
  initSynthesize();
}
