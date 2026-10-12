// @vitest-environment jsdom

import { beforeEach, describe, expect, it } from "vitest";

import {
  contentField,
  element,
  jsonResponse,
  listPayload,
  mount,
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

describe("templates panel: confirming a write", () => {
  it("writes only on confirm, then settles the draft on the new revision", async () => {
    const mounted = mount({
      "/api/templates": () => jsonResponse(listPayload([templateSummary()])),
      "/api/templates?name=Daily.md": () => jsonResponse(readPayload()),
      "/api/templates/preview": () => jsonResponse(previewPayload(false)),
      "/api/templates/apply": () =>
        jsonResponse(listPayload([], { action: "apply", name: "Daily.md", revision: "sha256:new" })),
    });

    await mounted.panel.load();
    openButtons(mounted.root)[0].click();
    await waitForExpect(() => expect(mounted.panel.draft().revision).toBe("sha256:old"));

    contentField(mounted.root).value = "# edited\n";
    element<HTMLButtonElement>(mounted.root, "[data-templates-save]").click();
    await waitForExpect(() => expect(mounted.panel.pending()).not.toBeNull());

    const diff = element<HTMLElement>(mounted.root, "[data-templates-diff]");
    expect(diff.hidden).toBe(false);
    expect(diff.textContent).toBe("+ # {{ .Title }}");
    expect(mounted.calls.map((call) => call.url)).not.toContain("/api/templates/apply");

    // The editor is inert while the receipt stands, so what is on screen is what
    // the diff describes. Typing past it anyway — which is what a stale
    // `disabled` would allow — must still not decide what the draft settles on.
    expect(contentField(mounted.root).disabled).toBe(true);
    contentField(mounted.root).value = "# typed past the diff\n";

    element<HTMLButtonElement>(mounted.root, "[data-templates-confirm]").click();
    await waitForExpect(() => expect(mounted.panel.draft().revision).toBe("sha256:new"));

    // The draft settles on the bytes the receipt carried — the ones the server
    // was asked to write — so a "Saved" message can never sit over bytes the
    // vault does not hold.
    expect(mounted.panel.draft().content).toBe("# edited\n");
    expect(contentField(mounted.root).value).toBe("# edited\n");
    expect(contentField(mounted.root).disabled).toBe(false);
    expect(diff.hidden).toBe(true);
    expect(statusText(mounted.root)).toBe('Saved "Daily.md".');
  });

  it("clears the draft after a delete and points at the undo", async () => {
    const mounted = mount({
      "/api/templates": () => jsonResponse(listPayload([templateSummary()])),
      "/api/templates?name=Daily.md": () => jsonResponse(readPayload()),
      "/api/templates/preview": () => jsonResponse(previewPayload(false)),
      "/api/templates/apply": () =>
        jsonResponse(listPayload([], { action: "apply", name: "Daily.md", removed: true })),
    });

    await mounted.panel.load();
    openButtons(mounted.root)[0].click();
    await waitForExpect(() => expect(mounted.panel.draft().revision).toBe("sha256:old"));

    element<HTMLButtonElement>(mounted.root, "[data-templates-delete]").click();
    await waitForExpect(() => expect(mounted.panel.pending()?.op).toBe("delete"));
    expect(mounted.calls[2]?.body).toMatchObject({ op: "delete", content: "", revision: "sha256:old" });

    element<HTMLButtonElement>(mounted.root, "[data-templates-confirm]").click();
    await waitForExpect(() => expect(statusText(mounted.root)).toContain("Deleted"));

    expect(mounted.panel.draft().name).toBe("");
    expect(contentField(mounted.root).value).toBe("");
    expect(statusText(mounted.root)).toContain("Undo it from Recent changes");
  });

  it("refuses a delete on a draft that names no bytes", async () => {
    const mounted = mount({ "/api/templates": () => jsonResponse(listPayload([])) });

    await mounted.panel.load();
    element<HTMLButtonElement>(mounted.root, "[data-templates-delete]").dispatchEvent(
      new MouseEvent("click", { bubbles: true })
    );
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(mounted.calls).toHaveLength(1);
    expect(statusText(mounted.root)).toBe("Open a template before deleting it.");
  });

});
