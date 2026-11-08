// @vitest-environment jsdom

import { beforeEach, describe, expect, it } from "vitest";
import { createSettingsPanel } from "./settings-controller";
import { jsonResponse, waitForExpect } from "./shared.test-helpers";
import { PANEL_HTML, button, control, listPayload, mount, previewPayload, rowFor, setting, statusText } from "./settings-controller.test-helpers";

beforeEach(() => {
  document.body.innerHTML = "";
  window.history.replaceState(null, "", "/settings");
});

describe("settings panel: writing", () => {
  it("previews the typed value, shows the diff, and applies only on confirm", async () => {
    const mounted = mount({
      "/api/settings": () => jsonResponse(listPayload([setting()])),
      "/api/settings/preview": () => jsonResponse(previewPayload()),
      "/api/settings/apply": () =>
        jsonResponse({ ...listPayload([setting()]), action: "set", journal_id: "j-1" }),
    });

    await mounted.panel.load();
    const row = rowFor(mounted.root, "capture.file");
    control(row).value = "  Inbox.md  ";
    button(row, "data-settings-save").click();
    await waitForExpect(() => expect(mounted.panel.pending()).not.toBeNull());

    expect(mounted.calls[1]?.url).toBe("/api/settings/preview");
    expect(mounted.calls[1]?.body).toMatchObject({
      op: "set",
      key: "capture.file",
      value: { kind: "string", text: "Inbox.md" },
    });
    const diff = mounted.root.querySelector("[data-settings-diff]") as HTMLElement;
    expect(diff.hidden).toBe(false);
    expect(diff.textContent).toBe('+ file = "Inbox.md"');
    const confirm = mounted.root.querySelector("[data-settings-confirm]") as HTMLButtonElement;
    expect(confirm.hidden).toBe(false);

    confirm.click();
    await waitForExpect(() => expect(statusText(mounted.root)).toBe('Saved "capture.file".'));

    expect(mounted.calls.map((call) => call.url)).toEqual([
      "/api/settings",
      "/api/settings/preview",
      "/api/settings/apply",
    ]);
    expect(mounted.panel.pending()).toBeNull();
    expect(diff.hidden).toBe(true);
    expect(confirm.hidden).toBe(true);
    expect(statusText(mounted.root)).toBe('Saved "capture.file".');
  });

  it("sends a checkbox as the bool it is", async () => {
    const mounted = mount({
      "/api/settings": () =>
        jsonResponse(
          listPayload([setting({ key: "sync.auto", kind: "bool", value: { kind: "bool", flag: false } })])
        ),
      "/api/settings/preview": () => jsonResponse(previewPayload()),
    });

    await mounted.panel.load();
    const row = rowFor(mounted.root, "sync.auto");
    (control(row) as HTMLInputElement).checked = true;
    button(row, "data-settings-save").click();
    await waitForExpect(() => expect(mounted.calls).toHaveLength(2));

    expect(mounted.calls[1]?.body).toMatchObject({
      value: { kind: "bool", flag: true },
    });
  });

  it("refuses a value of the wrong type without a round trip", async () => {
    const mounted = mount({
      "/api/settings": () =>
        jsonResponse(
          listPayload([setting({ key: "serve.port", kind: "int", value: { kind: "int", number: 8080 } })])
        ),
    });

    await mounted.panel.load();
    const row = rowFor(mounted.root, "serve.port");
    control(row).value = "eight thousand";
    button(row, "data-settings-save").click();
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(mounted.calls.map((call) => call.url)).toEqual(["/api/settings"]);
    expect(statusText(mounted.root)).toBe("This setting takes a whole number.");
    expect((mounted.root.querySelector("[data-settings-diff]") as HTMLElement).hidden).toBe(true);
  });

});

describe("settings panel: unset, cancel and refresh", () => {
  it("previews an unset with the key's own kind and an empty value", async () => {
    const mounted = mount({
      "/api/settings": () => jsonResponse(listPayload([setting()])),
      "/api/settings/preview": () => jsonResponse(previewPayload()),
    });

    await mounted.panel.load();
    button(rowFor(mounted.root, "capture.file"), "data-settings-unset").click();
    await waitForExpect(() => expect(mounted.panel.pending()).not.toBeNull());

    expect(mounted.calls[1]?.body).toMatchObject({
      op: "unset",
      key: "capture.file",
      value: { kind: "string", text: "", number: 0, flag: false, items: [] },
    });
    expect(statusText(mounted.root)).toContain('Remove "capture.file"');
  });

  it("re-renders the rows from the applied result, not from what was typed", async () => {
    const mounted = mount({
      "/api/settings": () => jsonResponse(listPayload([setting()])),
      "/api/settings/preview": () => jsonResponse(previewPayload()),
      "/api/settings/apply": () =>
        jsonResponse(
          listPayload([setting({ value: { kind: "string", text: "Inbox.md" }, assigned: true })])
        ),
    });

    await mounted.panel.load();
    const row = rowFor(mounted.root, "capture.file");
    control(row).value = "Inbox.md";
    button(row, "data-settings-save").click();
    await waitForExpect(() => expect(mounted.panel.pending()).not.toBeNull());
    (mounted.root.querySelector("[data-settings-confirm]") as HTMLButtonElement).click();
    await waitForExpect(() => expect(mounted.panel.pending()).toBeNull());

    const current = rowFor(mounted.root, "capture.file").querySelector("[data-settings-current]");
    expect(current?.textContent).toBe("Inbox.md");
  });

  it("drops the pending write when it is cancelled", async () => {
    const mounted = mount({
      "/api/settings": () => jsonResponse(listPayload([setting()])),
      "/api/settings/preview": () => jsonResponse(previewPayload()),
    });

    await mounted.panel.load();
    button(rowFor(mounted.root, "capture.file"), "data-settings-save").click();
    await waitForExpect(() => expect(mounted.panel.pending()).not.toBeNull());

    (mounted.root.querySelector("[data-settings-cancel]") as HTMLButtonElement).click();

    expect(mounted.panel.pending()).toBeNull();
    expect((mounted.root.querySelector("[data-settings-diff]") as HTMLElement).hidden).toBe(true);
    expect(statusText(mounted.root)).toBe("Change canceled. Nothing was changed.");
  });

  it("re-reads the table when Refresh is clicked", async () => {
    const mounted = mount({
      "/api/settings": () => jsonResponse(listPayload([setting()])),
    });

    await mounted.panel.load();
    (mounted.root.querySelector("[data-settings-refresh]") as HTMLButtonElement).click();
    await waitForExpect(() => expect(mounted.calls).toHaveLength(2));

    expect(mounted.calls.map((call) => call.url)).toEqual(["/api/settings", "/api/settings"]);
  });
});

describe("settings panel: draft continuity", () => {
  const pair = () => [setting(), setting({ key: "capture.heading", value: { kind: "string", text: "## Captured" } })];

  it("keeps another dirty field, its selection and focus through save and refresh", async () => {
    const mounted = mount({
      "/api/settings": () => jsonResponse(listPayload(pair())),
      "/api/settings/preview": () => jsonResponse(previewPayload()),
      "/api/settings/apply": () => jsonResponse(listPayload([
        setting({ value: { kind: "string", text: "New inbox.md" } }), pair()[1],
      ])),
    });
    await mounted.panel.load();
    const first = rowFor(mounted.root, "capture.file");
    const second = rowFor(mounted.root, "capture.heading");
    first.querySelector("details")!.open = true;
    second.querySelector("details")!.open = true;
    control(first).value = "New inbox.md";
    control(second).value = "## Unfinished heading";
    button(first, "data-settings-save").click();
    await waitForExpect(() => expect(mounted.panel.pending()).not.toBeNull());
    expect(first.contains(mounted.root.querySelector("[data-settings-confirm]"))).toBe(true);
    button(mounted.root, "data-settings-confirm").click();
    control(second).focus();
    control(second).setSelectionRange(3, 7);
    await waitForExpect(() => expect(statusText(mounted.root)).toContain("Saved"));
    expect(control(first).value).toBe("New inbox.md");
    expect(control(second).value).toBe("## Unfinished heading");
    expect(document.activeElement).toBe(control(second));
    expect(control(second).selectionStart).toBe(3);
    await mounted.panel.load();
    expect(control(second).value).toBe("## Unfinished heading");
    expect(document.activeElement).toBe(control(second));
    expect(control(second).selectionEnd).toBe(7);
    expect(second.dataset.dirty).toBe("true");
  });

  it("invalidates a preview when its input changes and keeps a reversion to the original value during apply", async () => {
    let finish!: (value: Response) => void;
    document.body.innerHTML = PANEL_HTML;
    const root = document.querySelector<HTMLElement>("[data-settings-panel]")!;
    const panel = createSettingsPanel({ root, token: "csrf", fetch: async (input) => {
      if (String(input).endsWith("/apply")) return new Promise<Response>((resolve) => { finish = resolve; });
      return jsonResponse(String(input).endsWith("/preview") ? previewPayload() : listPayload(pair()));
    } })!;
    await panel.load();
    const row = rowFor(root, "capture.file");
    control(row).value = "First.md";
    button(row, "data-settings-save").click();
    await waitForExpect(() => expect(panel.pending()).not.toBeNull());
    control(row).value = "Second.md";
    control(row).dispatchEvent(new Event("input"));
    expect(panel.pending()).toBeNull();
    button(row, "data-settings-save").click();
    await waitForExpect(() => expect(panel.pending()).not.toBeNull());
    button(root, "data-settings-confirm").click();
    control(row).value = "Inbox.md";
    control(row).dispatchEvent(new Event("input"));
    finish(jsonResponse(listPayload([setting({ value: { kind: "string", text: "Second.md" } }), pair()[1]])));
    await waitForExpect(() => expect(statusText(root)).toContain("Saved"));
    expect(control(row).value).toBe("Inbox.md");
    expect(row.querySelector("[data-settings-current]")?.textContent).toBe("Second.md");
  });
});

it("can reset an integer setting even while its unsaved input is invalid", async () => {
  const numberSetting = setting({ key: "cockpit.daily_limit", kind: "int", value: { kind: "int", number: 10 } });
  const mounted = mount({
    "/api/settings": () => jsonResponse(listPayload([numberSetting])),
    "/api/settings/preview": () => jsonResponse({ ...previewPayload(), settings: [numberSetting] }),
  });
  await mounted.panel.load();
  const row = rowFor(mounted.root, "cockpit.daily_limit");
  control(row).value = "";
  button(row, "data-settings-unset").click();
  await waitForExpect(() => expect(mounted.panel.pending()).not.toBeNull());
  expect(mounted.calls[1].body).toMatchObject({ key: "cockpit.daily_limit", op: "unset" });
});
