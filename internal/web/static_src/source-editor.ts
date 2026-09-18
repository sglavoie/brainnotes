import { normalizeSearchResults } from "./api";
import { queryElement } from "./dom";
import { createSourceEditorController } from "./source-editor-controller";
import {
  readSourceEditorConfig,
  readSourceEditorElements,
} from "./source-editor-elements";
import { createSourceSurfaceFactory } from "./source-editor-view";
import { createSourceClient } from "./source-save";
import { wikilinkOptions, wikilinkSearchURL } from "./source-wikilink";

// The editor's entry point: find the page's panel, build the pieces, load the
// note.
//
// It is the only module main.ts knows about, and it does no work of its own
// beyond assembly. A page without `[data-source-editor]` — which is every page
// but /edit — returns immediately, so the bundle stays one file.

export const initSourceEditor = (): void => {
  const panel = queryElement(document, "[data-source-editor]", HTMLElement);
  if (!panel) {
    return;
  }
  const config = readSourceEditorConfig(panel);
  const elements = readSourceEditorElements(panel);
  if (!config || !elements) {
    return;
  }

  const client = createSourceClient({
    endpoints: config.endpoints,
    key: config.key,
    token: config.token,
  });

  // The surface and the controller each need the other: the keymap calls the
  // controller, and the controller builds the surface. The factory closes over
  // a mutable reference rather than taking the controller, because a key cannot
  // be pressed before the editor it lives in exists.
  let controller: ReturnType<typeof createSourceEditorController> | null = null;
  const createSurface = createSourceSurfaceFactory({
    onSave: () => void controller?.save(),
    onCommand: (command, options) => controller?.runCommand(command, options),
    completeWikilink: async (query) => {
      const payload = await client.search(
        wikilinkSearchURL(config.endpoints.search, query)
      );
      return wikilinkOptions(normalizeSearchResults(payload));
    },
  });

  controller = createSourceEditorController({ elements, config, client, createSurface });
  void controller.load();
};
