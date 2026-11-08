// @vitest-environment jsdom

import { beforeEach, describe, expect, it } from "vitest";
import { createSettingsPanel } from "./settings-controller";
import { jsonResponse, waitForExpect } from "./shared.test-helpers";
import { button, control, listPayload, mount, previewPayload, rowFor, rows, setting, statusText } from "./settings-controller.test-helpers";

beforeEach(() => {
  document.body.innerHTML = "";
  window.history.replaceState(null, "", "/settings");
});

describe("settings panel: direct links", () => {
  it("focuses a linked setting after loading, only on the first successful load", async () => {
    window.history.replaceState(null, "", "/settings#setting-capture-inbox_note");
    const mounted = mount({
      "/api/settings": () => jsonResponse(listPayload([setting({ key: "capture.inbox_note" })])),
    });
    await mounted.panel.load();
    expect(document.activeElement).toBe(control(rowFor(mounted.root, "capture.inbox_note")));

    await mounted.panel.load();
    expect(document.activeElement).toBe(control(rowFor(mounted.root, "capture.inbox_note")));
  });

  it("keeps the user's focus when a linked setting finishes loading", async () => {
    window.history.replaceState(null, "", "/settings#setting-capture-inbox_note");
    const mounted = mount({
      "/api/settings": () => jsonResponse(listPayload([setting({ key: "capture.inbox_note" })])),
    });
    const loading = mounted.panel.load();
    const refresh = button(mounted.root, "data-settings-refresh");
    refresh.focus();
    await loading;
    expect(document.activeElement).toBe(refresh);
  });
});

describe("settings panel: rendering", () => {
  it("returns null on a page that is not the settings page", () => {
    document.body.innerHTML = "<div></div>";
    const panel = createSettingsPanel({
      root: document.body,
      fetch: async () => jsonResponse({}),
      token: "",
    });
    expect(panel).toBeNull();
  });

  it("renders exactly the keys the API served", async () => {
    // The machine-local overlay's keys are absent here because they are absent
    // there, not because this file filters them out.
    const mounted = mount({
      "/api/settings": () =>
        jsonResponse(
          listPayload([
            setting(),
            setting({ key: "sync.auto", kind: "bool", value: { kind: "bool", flag: true } }),
          ])
        ),
    });

    await mounted.panel.load();

    expect(rows(mounted.root).map((row) => row.getAttribute("data-settings-row"))).toEqual([
      "capture.file",
      "sync.auto",
    ]);
    expect(statusText(mounted.root)).toContain("2 vault settings");
    expect(statusText(mounted.root)).toContain("These settings travel with your notes.");
  });

  it("builds the control the declared kind calls for", async () => {
    const mounted = mount({
      "/api/settings": () =>
        jsonResponse(
          listPayload([
            setting({ key: "sync.auto", kind: "bool", value: { kind: "bool", flag: true } }),
            setting({ key: "serve.port", kind: "int", value: { kind: "int", number: 8080 } }),
            setting({
              key: "vault.ignore",
              kind: "string_list",
              value: { kind: "string_list", items: ["drafts", "archive"] },
            }),
            setting(),
          ])
        ),
    });

    await mounted.panel.load();

    const bool = control(rowFor(mounted.root, "sync.auto")) as HTMLInputElement;
    expect(bool.type).toBe("checkbox");
    expect(bool.checked).toBe(true);
    expect((control(rowFor(mounted.root, "serve.port")) as HTMLInputElement).type).toBe("number");
    const list = control(rowFor(mounted.root, "vault.ignore")) as HTMLTextAreaElement;
    expect(list.tagName).toBe("TEXTAREA");
    expect(list.value).toBe("drafts\narchive");
    expect((control(rowFor(mounted.root, "capture.file")) as HTMLInputElement).type).toBe("text");
  });

  it("says where each value came from and disables the reset a default has nothing to do", async () => {
    const mounted = mount({
      "/api/settings": () =>
        jsonResponse(listPayload([setting(), setting({ key: "sync.peer", assigned: false })])),
    });

    await mounted.panel.load();

    const assigned = rowFor(mounted.root, "capture.file");
    expect(assigned.querySelector("[data-settings-origin]")?.textContent).toBe("brainnotes.toml");
    expect(button(assigned, "data-settings-unset").disabled).toBe(false);

    const unassigned = rowFor(mounted.root, "sync.peer");
    expect(unassigned.querySelector("[data-settings-origin]")?.textContent).toBe("default");
    expect(button(unassigned, "data-settings-unset").disabled).toBe(true);
  });

  it("reports an empty table rather than an empty list", async () => {
    const mounted = mount({ "/api/settings": () => jsonResponse(listPayload([])) });

    await mounted.panel.load();

    expect(rows(mounted.root)).toHaveLength(0);
    expect(statusText(mounted.root)).toBe("No editable settings.");
  });

  it("surfaces a refused load and renders nothing", async () => {
    const mounted = mount({
      "/api/settings": () => jsonResponse({ ok: false, message: "settings: unavailable" }, 500),
    });

    await mounted.panel.load();

    expect(rows(mounted.root)).toHaveLength(0);
    expect(statusText(mounted.root)).toBe("settings: unavailable");
  });
});

describe("settings panel: filtering", () => {
  it("filters by group while keeping an unsaved edit visible", async () => {
    const mounted = mount({
      "/api/settings": () => jsonResponse(listPayload([
        setting(),
        setting({ key: "capture.inbox_note" }),
        setting({ key: "sync.auto", kind: "bool", value: { kind: "bool", flag: true } }),
      ])),
    });
    await mounted.panel.load();
    const edited = control(rowFor(mounted.root, "capture.file")) as HTMLInputElement;
    edited.value = "Draft.md";
    edited.dispatchEvent(new Event("input", { bubbles: true }));

    const filter = mounted.root.querySelector<HTMLInputElement>("[data-settings-filter]")!;
    filter.value = "sync";
    filter.dispatchEvent(new Event("input", { bubbles: true }));
    expect(rowFor(mounted.root, "capture.file").hidden).toBe(false);
    expect(rowFor(mounted.root, "capture.inbox_note").hidden).toBe(true);
    expect(rowFor(mounted.root, "sync.auto").hidden).toBe(false);
    expect(mounted.root.querySelector("[data-settings-filter-summary]")?.textContent).toContain("2 of 3");

    edited.value = "Inbox.md";
    edited.dispatchEvent(new Event("input", { bubbles: true }));
    expect(rowFor(mounted.root, "capture.file").hidden).toBe(true);
    expect(mounted.root.querySelector("[data-settings-filter-summary]")?.textContent).toContain("1 of 3");
  });

  it("does not hide the setting whose preview is waiting for confirmation", async () => {
    const mounted = mount({
      "/api/settings": () => jsonResponse(listPayload([
        setting(),
        setting({ key: "sync.auto", kind: "bool", value: { kind: "bool", flag: true } }),
      ])),
      "/api/settings/preview": () => jsonResponse(previewPayload()),
    });
    await mounted.panel.load();
    button(rowFor(mounted.root, "capture.file"), "data-settings-save").click();
    await waitForExpect(() => expect(mounted.panel.pending()).not.toBeNull());
    const filter = mounted.root.querySelector<HTMLInputElement>("[data-settings-filter]")!;
    filter.value = "sync";
    filter.dispatchEvent(new Event("input", { bubbles: true }));
    expect(rowFor(mounted.root, "capture.file").hidden).toBe(false);
    expect((mounted.root.querySelector("[data-settings-confirm]") as HTMLButtonElement).hidden).toBe(false);
  });

  it("keeps the setting just saved visible until the filter text changes", async () => {
    const mounted = mount({
      "/api/settings": () => jsonResponse(listPayload([
        setting(),
        setting({ key: "sync.auto", kind: "bool", value: { kind: "bool", flag: true } }),
      ])),
      "/api/settings/preview": () => jsonResponse(previewPayload()),
      "/api/settings/apply": () =>
        jsonResponse({ ...listPayload([setting()]), action: "set", journal_id: "j-1" }),
    });
    await mounted.panel.load();
    button(rowFor(mounted.root, "capture.file"), "data-settings-save").click();
    await waitForExpect(() => expect(mounted.panel.pending()).not.toBeNull());
    const filter = mounted.root.querySelector<HTMLInputElement>("[data-settings-filter]")!;
    filter.value = "sync";
    filter.dispatchEvent(new Event("input", { bubbles: true }));

    (mounted.root.querySelector("[data-settings-confirm]") as HTMLButtonElement).click();
    await waitForExpect(() => expect(mounted.panel.pending()).toBeNull());
    expect(rowFor(mounted.root, "capture.file").hidden).toBe(false);

    filter.value = "sync.";
    filter.dispatchEvent(new Event("input", { bubbles: true }));
    expect(rowFor(mounted.root, "capture.file").hidden).toBe(true);
  });
});

describe("settings panel: a shadowed key", () => {
  it("is bound to nothing, because the write could only be refused", async () => {
    const mounted = mount({
      "/api/settings": () =>
        jsonResponse(listPayload([setting({ layer: "local", shadowed: true })])),
    });

    await mounted.panel.load();
    const row = rowFor(mounted.root, "capture.file");

    expect(row.classList.contains("is-shadowed")).toBe(true);
    expect(control(row).disabled).toBe(true);
    expect(button(row, "data-settings-save").disabled).toBe(true);
    expect(button(row, "data-settings-unset").disabled).toBe(true);
    expect(row.querySelector("[data-settings-shadowed]")?.textContent).toContain(
      "brainnotes.local.toml"
    );

    // Even driven past the disabled attribute, no handler exists to issue the
    // request the server is certain to refuse.
    button(row, "data-settings-save").dispatchEvent(new MouseEvent("click", { bubbles: true }));
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(mounted.calls.map((call) => call.url)).toEqual(["/api/settings"]);
  });
});
