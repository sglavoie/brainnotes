// @vitest-environment jsdom

import { beforeEach, describe, expect, it } from "vitest";

import type { FetchLike } from "./settings-requests";
import { createTemplatesPanel } from "./templates-controller";

const jsonResponse = (body: unknown, status = 200): Response =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

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

/** The markup `templates/settings.html` renders for the templates panel. */
const PANEL_HTML = `
  <section data-templates-panel data-templates-csrf="csrf-token">
    <p data-templates-status></p>
    <p data-templates-notice></p>
    <ul data-templates-list></ul>
    <input type="text" data-templates-name>
    <textarea data-templates-content></textarea>
    <pre data-templates-diff hidden></pre>
    <button type="button" data-templates-save></button>
    <button type="button" data-templates-delete disabled></button>
    <button type="button" data-templates-confirm hidden></button>
    <button type="button" data-templates-cancel hidden></button>
    <button type="button" data-templates-new></button>
    <button type="button" data-templates-refresh></button>
  </section>
`;

// A real template's frontmatter holds unrendered actions, which is exactly why
// nothing on this page parses one: these bytes round-trip or the file stops
// being something `bn new` can render.
const DAILY = "---\ntitle: {{ .Title }}\ncreated: {{ today }}\n---\n\n# {{ .Title }}\n";

const templateSummary = (overrides: Record<string, unknown> = {}) => ({
  name: "Daily.md",
  rel_path: "Templates/Daily.md",
  size: DAILY.length,
  revision: "sha256:old",
  content: DAILY,
  ...overrides,
});

const listPayload = (templates: unknown[], overrides: Record<string, unknown> = {}) => ({
  ok: true,
  generation: 7,
  action: "list",
  dir: "Templates",
  extension: ".md",
  templates,
  diff: [],
  templating: { excluded_from_notes: true, portable: true },
  config: { generation: 7, source_hash: "h", status: "ok", restart_required: [], writable: true },
  ...overrides,
});

const readPayload = () =>
  listPayload([templateSummary()], { action: "read", name: "Daily.md", rel_path: "Templates/Daily.md" });

const previewPayload = (created: boolean) =>
  listPayload([], {
    action: "preview",
    name: "Daily.md",
    created,
    preview_id: "receipt-1",
    diff: [{ kind: "+", line: 1, text: "# {{ .Title }}" }],
  });

type Call = { url: string; body: unknown };

type Mounted = {
  root: HTMLElement;
  calls: Call[];
  panel: NonNullable<ReturnType<typeof createTemplatesPanel>>;
};

const mount = (routes: Record<string, () => Response>): Mounted => {
  document.body.innerHTML = PANEL_HTML;
  const root = document.querySelector("[data-templates-panel]") as HTMLElement;
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
  const panel = createTemplatesPanel({ root, fetch: fetcher, token: "csrf-token" });
  if (!panel) {
    throw new Error("panel did not mount");
  }
  return { root, calls, panel };
};

const element = <T extends HTMLElement>(root: HTMLElement, selector: string): T =>
  root.querySelector(selector) as T;

const nameField = (root: HTMLElement) => element<HTMLInputElement>(root, "[data-templates-name]");
const contentField = (root: HTMLElement) =>
  element<HTMLTextAreaElement>(root, "[data-templates-content]");
const statusText = (root: HTMLElement): string =>
  element<HTMLElement>(root, "[data-templates-status]").textContent || "";
const openButtons = (root: HTMLElement): HTMLButtonElement[] =>
  Array.from(root.querySelectorAll("[data-templates-open]"));

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
    expect(statusText(mounted.root)).toBe("Change cancelled.");
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
