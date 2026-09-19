import { queryElement } from "./dom";
import { createSettingsPanel } from "./settings-controller";
import { createTemplatesPanel } from "./templates-controller";

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
  const settingsRoot = queryElement(doc, "[data-settings-panel]", HTMLElement);
  if (settingsRoot) {
    const panel = createSettingsPanel({
      root: settingsRoot,
      fetch: (input, init) => window.fetch(input, init),
      token: settingsRoot.getAttribute("data-settings-csrf") || "",
    });
    void panel?.load();
  }

  const templatesRoot = queryElement(doc, "[data-templates-panel]", HTMLElement);
  if (templatesRoot) {
    const panel = createTemplatesPanel({
      root: templatesRoot,
      fetch: (input, init) => window.fetch(input, init),
      token: templatesRoot.getAttribute("data-templates-csrf") || "",
    });
    void panel?.load();
  }
};
