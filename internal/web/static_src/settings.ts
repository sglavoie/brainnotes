import { queryElement } from "./dom";
import { createSettingsPanel } from "./settings-controller";
import { createTemplatesPanel } from "./templates-controller";
import { warnBeforeUnload } from "./unload-guard";

type Panel = { load: () => Promise<void>; modified: () => boolean };

/**
 * Mounts the settings page.
 *
 * The two panels are independent surfaces that happen to share a page: one
 * writes lines in `brainnotes.toml`, the other writes files under the configured
 * templates directory, and neither reads the other's state. They are built
 * together only because the page hosting them is the one place a person goes to
 * change what this vault carries to its other replicas.
 *
 * Both are absent in read-only mode, where `main.ts` never calls this at all —
 * the server refuses the writes anyway, and offering controls that cannot
 * succeed is worse than offering none.
 */
export const initSettings = (doc: Document = document): void => {
  const fetcher = (input: RequestInfo | URL, init?: RequestInit) => window.fetch(input, init);
  const mounted = (
    selector: string,
    csrf: string,
    create: (root: HTMLElement, token: string) => Panel | null
  ): Panel | null => {
    const root = queryElement(doc, selector, HTMLElement);
    const panel = root ? create(root, root.getAttribute(csrf) || "") : null;
    void panel?.load();
    return panel;
  };
  const panels = [
    mounted("[data-settings-panel]", "data-settings-csrf", (root, token) =>
      createSettingsPanel({ root, fetch: fetcher, token })),
    mounted("[data-templates-panel]", "data-templates-csrf", (root, token) =>
      createTemplatesPanel({ root, fetch: fetcher, token })),
  ].filter((panel): panel is Panel => panel !== null);

  // Unsaved setting rows and template edits exist only in this tab.
  if (panels.length > 0) {
    warnBeforeUnload(doc.defaultView, () => panels.some((panel) => panel.modified()));
  }
};
