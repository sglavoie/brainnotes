// @vitest-environment jsdom

import { beforeEach, describe, expect, it } from "vitest";

import { createTemplatesPanel } from "./templates-controller";
import {
  DAILY,
  contentField,
  element,
  jsonResponse,
  listPayload,
  mount,
  nameField,
  openButtons,
  readPayload,
  statusText,
  templateSummary,
  waitForExpect,
} from "./templates-controller.test-helpers";

beforeEach(() => {
  document.body.innerHTML = "";
});

describe("templates panel: listing", () => {
  it("returns null on a page that is not the settings page", () => {
    document.body.innerHTML = "<div></div>";
    expect(
      createTemplatesPanel({ root: document.body, fetch: async () => jsonResponse({}), token: "" })
    ).toBeNull();
  });

  it("lists the directory as buttons, never as links", async () => {
    // A template has no page of its own and is never addressable by path, which
    // is the same reason the read endpoint takes a name in a query parameter.
    const mounted = mount({
      "/api/templates": () =>
        jsonResponse(listPayload([templateSummary(), templateSummary({ name: "Book.md" })])),
    });

    await mounted.panel.load();

    expect(openButtons(mounted.root).map((open) => open.textContent)).toEqual([
      "Daily.md",
      "Book.md",
    ]);
    expect(mounted.root.querySelectorAll("[data-templates-list] a")).toHaveLength(0);
    expect(statusText(mounted.root)).toBe("2 templates in Templates/.");
  });

  it("states both properties the directory has to have", async () => {
    const mounted = mount({
      "/api/templates": () =>
        jsonResponse(
          listPayload([], { templating: { excluded_from_notes: false, portable: false } })
        ),
    });

    await mounted.panel.load();

    expect(element<HTMLElement>(mounted.root, "[data-templates-notice]").textContent).toBe(
      "Templates/ is indexed as ordinary notes and does not synchronize."
    );
    expect(statusText(mounted.root)).toContain("No templates yet");
  });

  it("surfaces a refused listing", async () => {
    const mounted = mount({
      "/api/templates": () =>
        jsonResponse({ ok: false, code: "conflict", message: "templates: dir is hidden" }, 409),
    });

    await mounted.panel.load();

    expect(openButtons(mounted.root)).toHaveLength(0);
    expect(statusText(mounted.root)).toBe("templates: dir is hidden");
  });

  it("re-reads the directory when Refresh is clicked", async () => {
    const mounted = mount({ "/api/templates": () => jsonResponse(listPayload([])) });

    await mounted.panel.load();
    element<HTMLButtonElement>(mounted.root, "[data-templates-refresh]").click();
    await waitForExpect(() => expect(mounted.calls).toHaveLength(2));
  });
});

describe("templates panel: opening a draft", () => {
  it("asks for the template by name and loads its exact bytes", async () => {
    const mounted = mount({
      "/api/templates": () => jsonResponse(listPayload([templateSummary()])),
      "/api/templates?name=Daily.md": () => jsonResponse(readPayload()),
    });

    await mounted.panel.load();
    openButtons(mounted.root)[0].click();
    await waitForExpect(() => expect(mounted.panel.draft().revision).toBe("sha256:old"));

    expect(mounted.calls[1]?.url).toBe("/api/templates?name=Daily.md");
    expect(contentField(mounted.root).value).toBe(DAILY);
    expect(nameField(mounted.root).value).toBe("Daily.md");
    expect(statusText(mounted.root)).toBe("Editing Templates/Daily.md.");
  });

  it("percent-encodes the name it asks for", async () => {
    const mounted = mount({
      "/api/templates": () => jsonResponse(listPayload([templateSummary({ name: "Book Notes.md" })])),
      "/api/templates?name=Book%20Notes.md": () =>
        jsonResponse(listPayload([templateSummary({ name: "Book Notes.md" })], { action: "read" })),
    });

    await mounted.panel.load();
    openButtons(mounted.root)[0].click();
    await waitForExpect(() => expect(mounted.calls).toHaveLength(2));

    expect(mounted.calls[1]?.url).toBe("/api/templates?name=Book%20Notes.md");
  });

  it("enables the delete only once a draft names bytes that exist", async () => {
    const mounted = mount({
      "/api/templates": () => jsonResponse(listPayload([templateSummary()])),
      "/api/templates?name=Daily.md": () => jsonResponse(readPayload()),
    });

    await mounted.panel.load();
    const remove = element<HTMLButtonElement>(mounted.root, "[data-templates-delete]");
    expect(remove.disabled).toBe(true);

    openButtons(mounted.root)[0].click();
    await waitForExpect(() => expect(remove.disabled).toBe(false));

    element<HTMLButtonElement>(mounted.root, "[data-templates-new]").click();
    expect(remove.disabled).toBe(true);
    expect(mounted.panel.draft().revision).toBe("");
  });

  it("says so when the template is gone by the time it is opened", async () => {
    const mounted = mount({
      "/api/templates": () => jsonResponse(listPayload([templateSummary()])),
      "/api/templates?name=Daily.md": () => jsonResponse(listPayload([], { action: "read" })),
    });

    await mounted.panel.load();
    openButtons(mounted.root)[0].click();
    await waitForExpect(() =>
      expect(statusText(mounted.root)).toBe('"Daily.md" is no longer in the templates directory.')
    );
    expect(mounted.panel.draft().name).toBe("");
  });
});
