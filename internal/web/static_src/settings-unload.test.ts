// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import { initSettings } from "./settings";
import { jsonResponse, waitForExpect } from "./shared.test-helpers";
import {
  contentField,
  listPayload as templatesPayload,
  mount as mountTemplates,
  nameField,
  openButtons,
  readPayload,
  templateSummary,
} from "./templates-controller.test-helpers";
import { warnBeforeUnload } from "./unload-guard";

afterEach(() => {
  document.body.innerHTML = "";
  vi.unstubAllGlobals();
});

const unload = (): Event => {
  const event = new Event("beforeunload", { cancelable: true });
  window.dispatchEvent(event);
  return event;
};

const settingsPayload = {
  ok: true,
  generation: 7,
  action: "list",
  settings: [
    {
      key: "capture.file",
      kind: "string",
      summary: "Note quick captures are appended to.",
      assigned: true,
      layer: "portable",
      shadowed: false,
      value: { kind: "string", text: "Inbox.md" },
    },
  ],
  config_diff: [],
  config: { generation: 7, source_hash: "h", status: "ok", restart_required: [], writable: true },
};

const PAGE_HTML = `
  <section data-settings-panel data-settings-csrf="csrf-token">
    <p data-settings-status></p>
    <pre data-settings-diff hidden></pre>
    <button type="button" data-settings-confirm hidden></button>
    <button type="button" data-settings-cancel hidden></button>
    <ul data-settings-list></ul>
  </section>
  <section data-templates-panel data-templates-csrf="csrf-token">
    <p data-templates-status></p>
    <ul data-templates-list></ul>
    <input type="text" data-templates-name>
    <textarea data-templates-content></textarea>
    <pre data-templates-diff hidden></pre>
  </section>
`;

describe("warnBeforeUnload", () => {
  it("arms the prompt only while there are unsaved edits, until removed", () => {
    let unsaved = false;
    const remove = warnBeforeUnload(window, () => unsaved);
    expect(unload().defaultPrevented).toBe(false);
    unsaved = true;
    expect(unload().defaultPrevented).toBe(true);
    remove();
    expect(unload().defaultPrevented).toBe(false);
  });
});

describe("settings page unload warning", () => {
  it("warns for an unsaved setting row and for modified template text", async () => {
    document.body.innerHTML = PAGE_HTML;
    vi.stubGlobal("fetch", (input: RequestInfo | URL) =>
      Promise.resolve(
        jsonResponse(String(input) === "/api/settings" ? settingsPayload : templatesPayload([]))
      )
    );
    initSettings(document);
    await waitForExpect(() => expect(document.querySelector("[data-settings-control]")).not.toBeNull());
    expect(unload().defaultPrevented).toBe(false);

    const control = document.querySelector("[data-settings-control]") as HTMLInputElement;
    control.value = "Elsewhere.md";
    control.dispatchEvent(new Event("input", { bubbles: true }));
    expect(unload().defaultPrevented).toBe(true);
    control.value = "Inbox.md";
    control.dispatchEvent(new Event("input", { bubbles: true }));
    expect(unload().defaultPrevented).toBe(false);

    const content = document.querySelector("[data-templates-content]") as HTMLTextAreaElement;
    content.value = "typed but not saved";
    expect(unload().defaultPrevented).toBe(true);
  });
});

describe("templates panel modified()", () => {
  it("reports typed bytes and names, not an opened template as read", async () => {
    const mounted = mountTemplates({
      "/api/templates": () => jsonResponse(templatesPayload([templateSummary()])),
      "/api/templates?name=Daily.md": () => jsonResponse(readPayload()),
    });
    await mounted.panel.load();
    expect(mounted.panel.modified()).toBe(false);
    openButtons(mounted.root)[0]?.click();
    await waitForExpect(() => expect(mounted.panel.draft().revision).toBe("sha256:old"));
    expect(mounted.panel.modified()).toBe(false);
    contentField(mounted.root).value = "# changed\n";
    expect(mounted.panel.modified()).toBe(true);
    contentField(mounted.root).value = mounted.panel.draft().content;
    nameField(mounted.root).value = "Other.md";
    expect(mounted.panel.modified()).toBe(true);
  });
});
