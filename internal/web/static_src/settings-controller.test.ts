// @vitest-environment jsdom

import { beforeEach, describe, expect, it } from "vitest";

import { createSettingsPanel } from "./settings-controller";
import type { FetchLike } from "./settings-requests";

const jsonResponse = (body: unknown, status = 200): Response =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

/** The markup `templates/settings.html` renders for the settings panel. */
const PANEL_HTML = `
  <section data-settings-panel data-settings-csrf="csrf-token">
    <p data-settings-status></p>
    <pre data-settings-diff hidden></pre>
    <button type="button" data-settings-confirm hidden></button>
    <button type="button" data-settings-cancel hidden></button>
    <button type="button" data-settings-refresh></button>
    <ul data-settings-list></ul>
  </section>
`;

const setting = (overrides: Record<string, unknown> = {}) => ({
  key: "capture.file",
  kind: "string",
  summary: "Note quick captures are appended to.",
  assigned: true,
  layer: "portable",
  shadowed: false,
  value: { kind: "string", text: "Inbox.md" },
  ...overrides,
});

const listPayload = (settings: unknown[]) => ({
  ok: true,
  generation: 7,
  action: "list",
  settings,
  config_diff: [],
  config: { generation: 7, source_hash: "h", status: "ok", restart_required: [], writable: true },
});

const previewPayload = () => ({
  ...listPayload([setting()]),
  action: "set",
  key: "capture.file",
  preview_id: "receipt-1",
  config_diff: [{ kind: "+", line: 4, text: 'file = "Inbox.md"' }],
});

type Call = { url: string; body: unknown };

type Mounted = {
  root: HTMLElement;
  calls: Call[];
  panel: NonNullable<ReturnType<typeof createSettingsPanel>>;
};

const mount = (routes: Record<string, () => Response>, html = PANEL_HTML): Mounted => {
  document.body.innerHTML = html;
  const root = document.querySelector("[data-settings-panel]") as HTMLElement;
  const calls: Call[] = [];
  const fetcher: FetchLike = async (input, init) => {
    const url = String(input);
    calls.push({ url, body: typeof init?.body === "string" ? JSON.parse(init.body) : null });
    const route = routes[url];
    if (!route) {
      throw new Error(`unexpected request ${url}`);
    }
    return route();
  };
  const panel = createSettingsPanel({ root, fetch: fetcher, token: "csrf-token" });
  if (!panel) {
    throw new Error("panel did not mount");
  }
  return { root, calls, panel };
};

const rows = (root: HTMLElement): HTMLElement[] =>
  Array.from(root.querySelectorAll("[data-settings-row]"));

const rowFor = (root: HTMLElement, key: string): HTMLElement => {
  const row = root.querySelector(`[data-settings-row="${key}"]`);
  if (!(row instanceof HTMLElement)) {
    throw new Error(`no row for ${key}`);
  }
  return row;
};

const button = (row: HTMLElement, attribute: string): HTMLButtonElement =>
  row.querySelector(`[${attribute}]`) as HTMLButtonElement;

const control = (row: HTMLElement): HTMLInputElement | HTMLTextAreaElement =>
  row.querySelector("[data-settings-control]") as HTMLInputElement | HTMLTextAreaElement;

const statusText = (root: HTMLElement): string =>
  (root.querySelector("[data-settings-status]") as HTMLElement).textContent || "";

/**
 * Waits for a click-started round trip to finish.
 *
 * A click handler returns before the request it started, and reading a `Response`
 * body is itself several turns, so a fixed number of microtasks would be a guess.
 */
const waitForExpect = async (assertion: () => void): Promise<void> => {
  let lastError: unknown;
  for (let attempt = 0; attempt < 20; attempt += 1) {
    try {
      assertion();
      return;
    } catch (error) {
      lastError = error;
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
  }
  throw lastError;
};

beforeEach(() => {
  document.body.innerHTML = "";
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
    expect(statusText(mounted.root)).toContain("2 portable settings");
    expect(statusText(mounted.root)).toContain("brainnotes.local.toml");
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
    expect(statusText(mounted.root)).toBe("Change cancelled.");
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
