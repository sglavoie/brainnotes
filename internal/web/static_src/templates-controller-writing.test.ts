// @vitest-environment jsdom

import { beforeEach, describe, expect, it } from "vitest";

import {
  DAILY,
  contentField,
  element,
  jsonResponse,
  listPayload,
  mount,
  nameField,
  openButtons,
  previewPayload,
  readPayload,
  statusText,
  templateSummary,
  waitForExpect,
} from "./templates-controller.test-helpers";

beforeEach(() => {
  document.body.innerHTML = "";
});

describe("templates panel: writing", () => {
  it("pins a save to the revision the draft was read at", async () => {
    const mounted = mount({
      "/api/templates": () => jsonResponse(listPayload([templateSummary()])),
      "/api/templates?name=Daily.md": () => jsonResponse(readPayload()),
      "/api/templates/preview": () => jsonResponse(previewPayload(false)),
    });

    await mounted.panel.load();
    openButtons(mounted.root)[0].click();
    await waitForExpect(() => expect(mounted.panel.draft().revision).toBe("sha256:old"));

    contentField(mounted.root).value = `${DAILY}\n# extra\n`;
    element<HTMLButtonElement>(mounted.root, "[data-templates-save]").click();
    await waitForExpect(() => expect(mounted.panel.pending()).not.toBeNull());

    expect(mounted.calls[2]?.body).toEqual({
      token: "csrf-token",
      op: "save",
      name: "Daily.md",
      content: `${DAILY}\n# extra\n`,
      revision: "sha256:old",
    });
    expect(statusText(mounted.root)).toContain('Overwrite "Daily.md"');
  });

  it("drops the revision when the name is typed over, so the write is a create", async () => {
    // Renaming the draft addresses a different file, and the server refuses a
    // create whose destination exists — which is the answer a person wants
    // rather than a silent overwrite of a second file.
    const mounted = mount({
      "/api/templates": () => jsonResponse(listPayload([templateSummary()])),
      "/api/templates?name=Daily.md": () => jsonResponse(readPayload()),
      "/api/templates/preview": () => jsonResponse(previewPayload(true)),
    });

    await mounted.panel.load();
    openButtons(mounted.root)[0].click();
    await waitForExpect(() => expect(mounted.panel.draft().revision).toBe("sha256:old"));

    nameField(mounted.root).value = "Weekly.md";
    element<HTMLButtonElement>(mounted.root, "[data-templates-save]").click();
    await waitForExpect(() => expect(mounted.panel.pending()).not.toBeNull());

    expect(mounted.calls[2]?.body).toMatchObject({ name: "Weekly.md", revision: "" });
  });

  it("appends the configured extension to a bare stem", async () => {
    const mounted = mount({
      "/api/templates": () => jsonResponse(listPayload([], { extension: ".markdown" })),
      "/api/templates/preview": () => jsonResponse(previewPayload(true)),
    });

    await mounted.panel.load();
    nameField(mounted.root).value = "weekly";
    contentField(mounted.root).value = "# {{ .Title }}\n";
    element<HTMLButtonElement>(mounted.root, "[data-templates-save]").click();
    await waitForExpect(() => expect(mounted.calls).toHaveLength(2));

    expect(mounted.calls[1]?.body).toMatchObject({ name: "weekly.markdown" });
  });

  it("refuses a nameless save before any round trip", async () => {
    const mounted = mount({ "/api/templates": () => jsonResponse(listPayload([])) });

    await mounted.panel.load();
    contentField(mounted.root).value = "# orphan\n";
    element<HTMLButtonElement>(mounted.root, "[data-templates-save]").click();
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(mounted.calls).toHaveLength(1);
    expect(statusText(mounted.root)).toBe("Name the template before saving it.");
  });

});


describe("templates panel: abandoning a write", () => {
  it("drops the pending write when it is cancelled", async () => {
    const mounted = mount({
      "/api/templates": () => jsonResponse(listPayload([])),
      "/api/templates/preview": () => jsonResponse(previewPayload(true)),
    });

    await mounted.panel.load();
    nameField(mounted.root).value = "Weekly.md";
    contentField(mounted.root).value = "# {{ .Title }}\n";
    element<HTMLButtonElement>(mounted.root, "[data-templates-save]").click();
    await waitForExpect(() => expect(mounted.panel.pending()).not.toBeNull());

    element<HTMLButtonElement>(mounted.root, "[data-templates-cancel]").click();

    expect(mounted.panel.pending()).toBeNull();
    expect(element<HTMLElement>(mounted.root, "[data-templates-diff]").hidden).toBe(true);
    expect(statusText(mounted.root)).toBe("Change canceled. Nothing was changed.");
  });

  it("reports bytes the file already holds without offering a confirmation", async () => {
    const mounted = mount({
      "/api/templates": () => jsonResponse(listPayload([templateSummary()])),
      "/api/templates?name=Daily.md": () => jsonResponse(readPayload()),
      "/api/templates/preview": () => jsonResponse(listPayload([], { preview_id: "" })),
    });

    await mounted.panel.load();
    openButtons(mounted.root)[0].click();
    await waitForExpect(() => expect(mounted.panel.draft().revision).toBe("sha256:old"));

    element<HTMLButtonElement>(mounted.root, "[data-templates-save]").click();
    await waitForExpect(() =>
      expect(statusText(mounted.root)).toBe('"Daily.md" already holds exactly those bytes.')
    );

    expect(mounted.panel.pending()).toBeNull();
    expect(element<HTMLButtonElement>(mounted.root, "[data-templates-confirm]").hidden).toBe(true);
  });

  it("clears the pending write before starting a new draft", async () => {
    const mounted = mount({
      "/api/templates": () => jsonResponse(listPayload([])),
      "/api/templates/preview": () => jsonResponse(previewPayload(true)),
    });

    await mounted.panel.load();
    nameField(mounted.root).value = "Weekly.md";
    element<HTMLButtonElement>(mounted.root, "[data-templates-save]").click();
    await waitForExpect(() => expect(mounted.panel.pending()).not.toBeNull());

    element<HTMLButtonElement>(mounted.root, "[data-templates-new]").click();

    // A receipt aimed at the previous draft must not outlive it: it is one-use
    // and describes a destination the editor no longer shows.
    expect(mounted.panel.pending()).toBeNull();
    expect(nameField(mounted.root).value).toBe("");
    expect(statusText(mounted.root)).toBe("Name the new template and save it.");
  });
});
