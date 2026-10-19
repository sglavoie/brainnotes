import { unansweredRequest } from "./request-outcome";
import { describe, expect, it } from "vitest";

import { createSettingsWriter } from "./settings-actions";
import { settingValueOf } from "./settings-model";
import { createTemplatesWriter } from "./templates-actions";
import type { StatusKind } from "./status";

const jsonResponse = (body: unknown, status = 200): Response =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

type Call = { url: string; token: string | null; body: unknown };

type Harness<W> = {
  calls: Call[];
  statuses: Array<{ message: string; kind: StatusKind }>;
  pendings: unknown[];
  saved: unknown[];
  writer: W;
};

const recorder = (routes: Record<string, () => Response>, calls: Call[]) => {
  return async (input: string, init?: RequestInit): Promise<Response> => {
    const url = String(input);
    const headers = new Headers(init?.headers);
    const body = typeof init?.body === "string" ? JSON.parse(init.body) : null;
    calls.push({ url, token: headers.get("X-BN-CSRF"), body });
    const route = routes[url];
    if (!route) {
      throw new Error(`unexpected request ${url}`);
    }
    return route();
  };
};

const settingsPayload = (overrides: Record<string, unknown> = {}) => ({
  ok: true,
  generation: 7,
  action: "set",
  key: "capture.file",
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
  ...overrides,
});

const settingsPreview = () =>
  settingsPayload({
    preview_id: "receipt-1",
    expires_at: "2026-09-18T05:00:00Z",
    config_diff: [
      { kind: "-", line: 4, text: 'file = "Notes.md"' },
      { kind: "+", line: 4, text: 'file = "Inbox.md"' },
    ],
  });

const startSettings = (
  routes: Record<string, () => Response>
): Harness<ReturnType<typeof createSettingsWriter>> => {
  const calls: Call[] = [];
  const statuses: Array<{ message: string; kind: StatusKind }> = [];
  const pendings: unknown[] = [];
  const saved: unknown[] = [];
  const writer = createSettingsWriter({
    fetch: recorder(routes, calls),
    token: "csrf-token",
    hooks: {
      onStatus: (message, kind) => statuses.push({ message, kind }),
      onPending: (pending) => pendings.push(pending),
      onSaved: (result) => saved.push(result),
    },
  });
  return { calls, statuses, pendings, saved, writer };
};

const lastStatus = (harness: Harness<unknown>) => harness.statuses[harness.statuses.length - 1];

const inboxValue = () => settingValueOf("string", { text: "Inbox.md" });

describe("settings writer", () => {
  it("stops on the rendered diff and writes nothing until it is confirmed", async () => {
    const harness = startSettings({
      "/api/settings/preview": () => jsonResponse(settingsPreview()),
      "/api/settings/apply": () => jsonResponse(settingsPayload({ journal_id: "j-1" })),
    });

    await harness.writer.preview("capture.file", "set", inboxValue());

    // Unlike a saved view's create, nothing here auto-chains: a setting always
    // replaces or removes a line in a file the vault already loads.
    expect(harness.calls.map((call) => call.url)).toEqual(["/api/settings/preview"]);
    expect(harness.writer.pending()?.previewID).toBe("receipt-1");
    expect(harness.writer.pending()?.diff).toHaveLength(2);
    expect(lastStatus(harness)?.kind).toBe("is-warning");
    expect(harness.saved).toHaveLength(0);

    await harness.writer.confirm();

    expect(harness.calls.map((call) => call.url)).toEqual([
      "/api/settings/preview",
      "/api/settings/apply",
    ]);
    expect(harness.calls[1]?.body).toMatchObject({
      preview_id: "receipt-1",
      operation_id: expect.stringMatching(/^[0-9a-f]{32}$/),
    });
    expect(harness.saved).toHaveLength(1);
    expect(harness.writer.pending()).toBeNull();
    expect(lastStatus(harness)).toEqual({ message: 'Saved "capture.file".', kind: "is-success" });
  });

  it("sends the key, the op and the typed value with the token in header and body", async () => {
    const harness = startSettings({
      "/api/settings/preview": () => jsonResponse(settingsPreview()),
    });

    await harness.writer.preview("capture.file", "set", inboxValue());

    expect(harness.calls[0]?.token).toBe("csrf-token");
    expect(harness.calls[0]?.body).toEqual({
      token: "csrf-token",
      op: "set",
      key: "capture.file",
      value: { kind: "string", text: "Inbox.md", number: 0, flag: false, items: [] },
    });
  });

  it("reports a no-op without issuing a receipt", async () => {
    const harness = startSettings({
      "/api/settings/preview": () => jsonResponse(settingsPayload({ preview_id: "" })),
    });

    await harness.writer.preview("capture.file", "set", inboxValue());

    expect(harness.writer.pending()).toBeNull();
    expect(lastStatus(harness)?.message).toBe('"capture.file" already holds that value.');
    expect(lastStatus(harness)?.kind).toBe("");
  });

  it("drops the receipt when the apply is refused, so a retry re-previews", async () => {
    const harness = startSettings({
      "/api/settings/preview": () => jsonResponse(settingsPreview()),
      "/api/settings/apply": () =>
        jsonResponse({ ok: false, message: "settings: brainnotes.toml changed" }, 409),
    });

    await harness.writer.preview("capture.file", "set", inboxValue());
    await harness.writer.confirm();

    expect(harness.writer.pending()).toBeNull();
    expect(lastStatus(harness)).toEqual({
      message: "settings: brainnotes.toml changed",
      kind: "is-error",
    });

    // A receipt is one-use, so the second confirmation must not aim at the one
    // the server already consumed.
    await harness.writer.confirm();
    expect(harness.calls.map((call) => call.url)).toEqual([
      "/api/settings/preview",
      "/api/settings/apply",
    ]);
  });

});

describe("settings writer: receipts", () => {
  it("holds exactly one receipt: a second preview replaces the first", async () => {
    const harness = startSettings({
      "/api/settings/preview": () => jsonResponse(settingsPreview()),
    });

    await harness.writer.preview("capture.file", "set", inboxValue());
    await harness.writer.preview("capture.heading", "unset", settingValueOf("string", {}));

    expect(harness.pendings.filter((entry) => entry === null)).toHaveLength(2);
    expect(harness.writer.pending()?.key).toBe("capture.heading");
    expect(harness.writer.pending()?.op).toBe("unset");
  });

  it("refuses a confirmation with no receipt", async () => {
    const harness = startSettings({});

    await harness.writer.confirm();

    expect(harness.calls).toHaveLength(0);
    expect(lastStatus(harness)).toEqual({
      message: "Preview the change before saving it.",
      kind: "is-error",
    });
  });

  it("surfaces a refused preview and issues no apply", async () => {
    const harness = startSettings({
      "/api/settings/preview": () =>
        jsonResponse({ ok: false, message: "settings: capture.heading must be an ATX heading" }, 400),
    });

    await harness.writer.preview("capture.heading", "set", settingValueOf("string", { text: "x" }));

    expect(harness.calls.map((call) => call.url)).toEqual(["/api/settings/preview"]);
    expect(harness.writer.pending()).toBeNull();
    expect(lastStatus(harness)?.message).toContain("ATX heading");
  });

  it("drops the receipt on cancel and says nothing when there was none", async () => {
    const harness = startSettings({
      "/api/settings/preview": () => jsonResponse(settingsPreview()),
    });

    harness.writer.cancel();
    expect(harness.statuses).toHaveLength(0);

    await harness.writer.preview("capture.file", "set", inboxValue());
    harness.writer.cancel();

    expect(harness.writer.pending()).toBeNull();
    expect(lastStatus(harness)?.message).toBe("Change cancelled.");
  });
});

const templatesPayload = (overrides: Record<string, unknown> = {}) => ({
  ok: true,
  generation: 7,
  action: "save",
  name: "Daily.md",
  rel_path: "Templates/Daily.md",
  dir: "Templates",
  extension: ".md",
  templates: [],
  diff: [],
  revision: "sha256:new",
  templating: { excluded_from_notes: true, portable: true },
  config: { generation: 7, source_hash: "h", status: "ok", restart_required: [], writable: true },
  ...overrides,
});

const templatesPreview = (created: boolean) =>
  templatesPayload({
    created,
    preview_id: "receipt-1",
    diff: [{ kind: "+", line: 1, text: "# {{ .Title }}" }],
  });

const startTemplates = (
  routes: Record<string, () => Response>
): Harness<ReturnType<typeof createTemplatesWriter>> => {
  const calls: Call[] = [];
  const statuses: Array<{ message: string; kind: StatusKind }> = [];
  const pendings: unknown[] = [];
  const saved: unknown[] = [];
  const writer = createTemplatesWriter({
    fetch: recorder(routes, calls),
    token: "csrf-token",
    hooks: {
      onStatus: (message, kind) => statuses.push({ message, kind }),
      onPending: (pending) => pendings.push(pending),
      onSaved: (pending, result) => saved.push({ pending, result }),
    },
  });
  return { calls, statuses, pendings, saved, writer };
};

describe("templates writer", () => {
  it("stops a create on its diff, unlike a saved view's create", async () => {
    const harness = startTemplates({
      "/api/templates/preview": () => jsonResponse(templatesPreview(true)),
      "/api/templates/apply": () => jsonResponse(templatesPayload({ journal_id: "j-1" })),
    });

    await harness.writer.preview({
      op: "save",
      name: "Daily.md",
      content: "# {{ .Title }}\n",
      revision: "",
    });

    // A template is a whole file, so the preview is the only place a person sees
    // the bytes that are about to become one.
    expect(harness.calls.map((call) => call.url)).toEqual(["/api/templates/preview"]);
    expect(harness.writer.pending()?.created).toBe(true);
    expect(lastStatus(harness)?.message).toContain('Create "Daily.md"');

    await harness.writer.confirm();

    expect(harness.saved).toHaveLength(1);
    expect(lastStatus(harness)).toEqual({ message: 'Created "Daily.md".', kind: "is-success" });
  });

  it("sends the revision the edit was planned against", async () => {
    const harness = startTemplates({
      "/api/templates/preview": () => jsonResponse(templatesPreview(false)),
    });

    await harness.writer.preview({
      op: "save",
      name: "Daily.md",
      content: "# edited\n",
      revision: "sha256:old",
    });

    expect(harness.calls[0]?.body).toEqual({
      token: "csrf-token",
      op: "save",
      name: "Daily.md",
      content: "# edited\n",
      revision: "sha256:old",
    });
  });

  it("carries no content on a delete, whatever the caller passed", async () => {
    const harness = startTemplates({
      "/api/templates/preview": () => jsonResponse(templatesPreview(false)),
    });

    await harness.writer.preview({
      op: "delete",
      name: "Daily.md",
      content: "bytes the editor still holds",
      revision: "sha256:old",
    });

    expect(harness.calls[0]?.body).toMatchObject({ op: "delete", content: "" });
    expect(lastStatus(harness)?.message).toContain('Delete "Daily.md"');
  });

  it("hands the redeemed receipt to the caller so it can settle the draft", async () => {
    const harness = startTemplates({
      "/api/templates/preview": () => jsonResponse(templatesPreview(false)),
      "/api/templates/apply": () => jsonResponse(templatesPayload({ revision: "sha256:new" })),
    });

    await harness.writer.preview({
      op: "save",
      name: "Daily.md",
      content: "# edited\n",
      revision: "sha256:old",
    });
    await harness.writer.confirm();

    expect(harness.saved).toHaveLength(1);
    const entry = harness.saved[0] as { pending: { name: string }; result: { revision: string } };
    expect(entry.pending.name).toBe("Daily.md");
    expect(entry.result.revision).toBe("sha256:new");
  });

});

describe("templates writer: refusals", () => {
  it("reports bytes the file already holds without issuing a receipt", async () => {
    const harness = startTemplates({
      "/api/templates/preview": () => jsonResponse(templatesPayload({ preview_id: "" })),
    });

    await harness.writer.preview({
      op: "save",
      name: "Daily.md",
      content: "# unchanged\n",
      revision: "sha256:old",
    });

    expect(harness.writer.pending()).toBeNull();
    expect(lastStatus(harness)?.message).toBe('"Daily.md" already holds exactly those bytes.');
  });

  it("surfaces a stale preview and keeps no receipt", async () => {
    const harness = startTemplates({
      "/api/templates/preview": () => jsonResponse(templatesPreview(false)),
      "/api/templates/apply": () =>
        jsonResponse({ ok: false, code: "preview_stale", message: "templates: the file changed" }, 409),
    });

    await harness.writer.preview({
      op: "save",
      name: "Daily.md",
      content: "# edited\n",
      revision: "sha256:old",
    });
    await harness.writer.confirm();

    expect(harness.writer.pending()).toBeNull();
    expect(harness.saved).toHaveLength(0);
    expect(lastStatus(harness)?.kind).toBe("is-error");
  });

  it("answers a transport failure rather than throwing at the caller", async () => {
    const calls: Call[] = [];
    const statuses: Array<{ message: string; kind: StatusKind }> = [];
    const writer = createTemplatesWriter({
      fetch: async (input) => {
        calls.push({ url: String(input), token: null, body: null });
        throw new TypeError("network down");
      },
      token: "csrf-token",
      hooks: { onStatus: (message, kind) => statuses.push({ message, kind }) },
    });

    await writer.preview({ op: "save", name: "Daily.md", content: "x", revision: "" });

    expect(writer.pending()).toBeNull();
    expect(writer.busy()).toBe(false);
    expect(statuses[statuses.length - 1]).toEqual({
      message: unansweredRequest("preview").message,
      kind: "is-error",
    });
  });
});
