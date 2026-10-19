import { describe, expect, it } from "vitest";

import type { SavedViewsResult } from "./api-types";
import { createSavedViewSaver } from "./saved-views-actions";
import type {
  SavedViewCreateReceipt,
  SavedViewPendingSave,
  SavedViewSaveInput,
} from "./saved-views-model";
import type { StatusKind } from "./status";

const jsonResponse = (body: unknown, status = 200): Response =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

const savedViewsPayload = (overrides: Record<string, unknown> = {}) => ({
  ok: true,
  generation: 4,
  action: "upsert",
  name: "Focus",
  views: [
    {
      name: "Focus",
      query: "tag:focus",
      normalized: "tag:focus",
      enabled: true,
    },
  ],
  created: false,
  config_diff: [],
  config: { generation: 4, source_hash: "abc", status: "ok", restart_required: [], writable: true },
  ...overrides,
});

const previewPayload = (created: boolean) =>
  savedViewsPayload({
    created,
    preview_id: "receipt-1",
    expires_at: "2026-08-26T05:00:00Z",
    config_diff: created
      ? [{ kind: "+", line: 12, text: '[[views]]' }, { kind: "+", line: 13, text: 'name = "Focus"' }]
      : [
          { kind: "-", line: 13, text: 'query = "tag:old"' },
          { kind: "+", line: 13, text: 'query = "tag:focus"' },
        ],
  });

const applyPayload = () => savedViewsPayload({ journal_id: "j-1" });

type Call = { url: string; body: unknown };

type Harness = {
  calls: Call[];
  statuses: Array<{ message: string; kind: StatusKind }>;
  pendings: Array<SavedViewPendingSave | null>;
  receipts: Array<SavedViewCreateReceipt | null>;
  saved: SavedViewsResult[];
  saver: ReturnType<typeof createSavedViewSaver>;
};

const startSaver = (
  routes: Record<string, () => Response>,
  token = "csrf-token"
): Harness => {
  const calls: Call[] = [];
  const statuses: Array<{ message: string; kind: StatusKind }> = [];
  const pendings: Array<SavedViewPendingSave | null> = [];
  const receipts: Array<SavedViewCreateReceipt | null> = [];
  const saved: SavedViewsResult[] = [];

  const fetcher = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const url = String(input);
    const body = typeof init?.body === "string" ? JSON.parse(init.body) : null;
    calls.push({ url, body });
    const route = routes[url];
    if (!route) {
      throw new Error(`unexpected request ${url}`);
    }
    return route();
  };

  const saver = createSavedViewSaver({
    fetch: fetcher,
    token,
    hooks: {
      onStatus: (message, kind) => statuses.push({ message, kind }),
      onPending: (pending) => pendings.push(pending),
      onReceipt: (receipt) => receipts.push(receipt),
      onSaved: (result) => saved.push(result),
    },
  });
  return { calls, statuses, pendings, receipts, saved, saver };
};

const focusInput = (overrides: Partial<SavedViewSaveInput> = {}): SavedViewSaveInput => ({
  name: "Focus",
  description: "Active focus work",
  query: "tag:focus",
  sort: "-modified",
  relativeTo: "",
  limit: 0,
  ...overrides,
});

const lastStatus = (harness: Harness) => harness.statuses[harness.statuses.length - 1];

describe("saved view save: create", () => {
  it("chains preview straight into apply when the view does not exist yet", async () => {
    const harness = startSaver({
      "/api/views/preview": () => jsonResponse(previewPayload(true)),
      "/api/views/apply": () => jsonResponse(applyPayload()),
    });

    await harness.saver.save(focusInput());

    expect(harness.calls.map((call) => call.url)).toEqual([
      "/api/views/preview",
      "/api/views/apply",
    ]);
    expect(harness.calls[1]?.body).toEqual({ preview_id: "receipt-1", operation_id: expect.stringMatching(/^[0-9a-f]{32}$/) });
    expect(harness.saved).toHaveLength(1);
    expect(harness.saver.pending()).toBeNull();
    // Nothing pending is ever reported to the UI: there is nothing to confirm.
    expect(harness.pendings.every((pending) => pending === null)).toBe(true);
    expect(lastStatus(harness)).toEqual({ message: 'Saved view "Focus".', kind: "is-success" });
  });

  it("shows the create diff as a receipt once the auto-applied write landed", async () => {
    const harness = startSaver({
      "/api/views/preview": () => jsonResponse(previewPayload(true)),
      "/api/views/apply": () => jsonResponse(applyPayload()),
    });

    await harness.saver.save(focusInput());

    // The diff previewed for a create is published, not discarded: it is the
    // only chance the user gets to see what the auto-chained write did.
    const receipt = harness.saver.receipt();
    expect(receipt).toEqual({
      name: "Focus",
      diff: [
        { kind: "+", line: 12, text: "[[views]]" },
        { kind: "+", line: 13, text: 'name = "Focus"' },
      ],
    });
    expect(harness.receipts).toEqual([receipt]);
  });

  it("publishes the create receipt only after the apply succeeded", async () => {
    const harness = startSaver({
      "/api/views/preview": () => jsonResponse(previewPayload(true)),
      "/api/views/apply": () =>
        jsonResponse({ ok: false, message: "views: brainnotes.toml changed" }, 409),
    });

    await harness.saver.save(focusInput());

    expect(harness.saver.receipt()).toBeNull();
    expect(harness.receipts).toEqual([]);
    expect(lastStatus(harness)?.kind).toBe("is-error");
  });
});

describe("saved view save: create receipts", () => {
  it("issues a non-actionable create receipt that cannot re-apply the consumed preview", async () => {
    const harness = startSaver({
      "/api/views/preview": () => jsonResponse(previewPayload(true)),
      "/api/views/apply": () => jsonResponse(applyPayload()),
    });

    await harness.saver.save(focusInput());

    // The receipt carries no preview ID at all, so there is no handle to apply
    // a second time...
    expect(Object.keys(harness.saver.receipt() || {}).sort()).toEqual(["diff", "name"]);
    // ...and confirming with only a create receipt in hand issues no write.
    await harness.saver.confirm();

    expect(harness.calls.map((call) => call.url)).toEqual([
      "/api/views/preview",
      "/api/views/apply",
    ]);
    expect(harness.saved).toHaveLength(1);
    expect(lastStatus(harness)).toEqual({
      message: "Preview the view before saving it.",
      kind: "is-error",
    });
  });

  it("drops the previous create receipt when the next save starts", async () => {
    const harness = startSaver({
      "/api/views/preview": () => jsonResponse(previewPayload(true)),
      "/api/views/apply": () => jsonResponse(applyPayload()),
    });

    await harness.saver.save(focusInput());
    expect(harness.saver.receipt()).not.toBeNull();

    await harness.saver.save(focusInput({ name: "Other" }));

    // Cleared as the save starts, then replaced by the new write's own diff —
    // the block never shows a receipt from a save two writes ago.
    expect(harness.receipts.map((entry) => entry?.name ?? null)).toEqual([
      "Focus",
      null,
      "Other",
    ]);
  });

  it("previews the stored fields the URL carried", async () => {
    const harness = startSaver({
      "/api/views/preview": () => jsonResponse(previewPayload(true)),
      "/api/views/apply": () => jsonResponse(applyPayload()),
    });

    await harness.saver.save(focusInput({ relativeTo: "areas/hub" }));

    expect(harness.calls[0]?.body).toEqual({
      token: "csrf-token",
      op: "upsert",
      name: "Focus",
      view: {
        query: "tag:focus",
        sort: "-modified",
        relative_to: "areas/hub",
        limit: 0,
        description: "Active focus work",
      },
    });
  });
});

describe("saved view save: overwrite", () => {
  it("stops on the rendered diff and writes nothing until it is confirmed", async () => {
    const harness = startSaver({
      "/api/views/preview": () => jsonResponse(previewPayload(false)),
      "/api/views/apply": () => jsonResponse(applyPayload()),
    });

    await harness.saver.save(focusInput());

    expect(harness.calls.map((call) => call.url)).toEqual(["/api/views/preview"]);
    const pending = harness.saver.pending();
    expect(pending?.previewID).toBe("receipt-1");
    expect(pending?.created).toBe(false);
    expect(pending?.diff).toHaveLength(2);
    expect(harness.saved).toHaveLength(0);
    expect(lastStatus(harness)?.kind).toBe("is-warning");
    expect(lastStatus(harness)?.message).toContain("confirm the overwrite");

    await harness.saver.confirm();

    expect(harness.calls.map((call) => call.url)).toEqual([
      "/api/views/preview",
      "/api/views/apply",
    ]);
    expect(harness.saved).toHaveLength(1);
    expect(harness.saver.pending()).toBeNull();
    expect(lastStatus(harness)).toEqual({ message: 'Updated view "Focus".', kind: "is-success" });
    // An overwrite already showed its diff as the question it confirmed, so it
    // publishes no create receipt afterwards.
    expect(harness.saver.receipt()).toBeNull();
    expect(harness.receipts).toEqual([]);
  });

  it("drops the receipt when the overwrite is cancelled", async () => {
    const harness = startSaver({
      "/api/views/preview": () => jsonResponse(previewPayload(false)),
    });

    await harness.saver.save(focusInput());
    expect(harness.saver.pending()).not.toBeNull();

    harness.saver.cancel();

    expect(harness.saver.pending()).toBeNull();
    expect(lastStatus(harness)?.message).toBe("Overwrite cancelled.");
    expect(harness.calls).toHaveLength(1);
  });

  it("refuses a confirmation with no receipt", async () => {
    const harness = startSaver({});

    await harness.saver.confirm();

    expect(harness.calls).toHaveLength(0);
    expect(lastStatus(harness)).toEqual({
      message: "Preview the view before saving it.",
      kind: "is-error",
    });
  });
});

describe("saved view save: refusals", () => {
  it("reports a no-op upsert without issuing a write", async () => {
    const harness = startSaver({
      "/api/views/preview": () => jsonResponse(savedViewsPayload({ created: false })),
    });

    await harness.saver.save(focusInput());

    expect(harness.calls.map((call) => call.url)).toEqual(["/api/views/preview"]);
    expect(harness.saver.pending()).toBeNull();
    expect(lastStatus(harness)?.message).toContain("already saved exactly like this");
  });

  it("surfaces a refused preview and issues no apply", async () => {
    const harness = startSaver({
      "/api/views/preview": () =>
        jsonResponse({ ok: false, message: 'views: unknown field "statuz"' }, 400),
    });

    await harness.saver.save(focusInput({ query: "statuz:wip" }));

    expect(harness.calls.map((call) => call.url)).toEqual(["/api/views/preview"]);
    expect(harness.saver.pending()).toBeNull();
    expect(lastStatus(harness)).toEqual({
      message: 'views: unknown field "statuz"',
      kind: "is-error",
    });
  });

  it("drops the receipt when the apply is refused, so a retry re-previews", async () => {
    const harness = startSaver({
      "/api/views/preview": () => jsonResponse(previewPayload(false)),
      "/api/views/apply": () =>
        jsonResponse({ ok: false, message: "views: brainnotes.toml changed" }, 409),
    });

    await harness.saver.save(focusInput());
    await harness.saver.confirm();

    expect(harness.saver.pending()).toBeNull();
    expect(lastStatus(harness)).toEqual({
      message: "views: brainnotes.toml changed",
      kind: "is-error",
    });

    await harness.saver.confirm();
    expect(harness.calls.map((call) => call.url)).toEqual([
      "/api/views/preview",
      "/api/views/apply",
    ]);
  });

  it("refuses to save without a CSRF token", async () => {
    const harness = startSaver({}, "");

    await harness.saver.save(focusInput());

    expect(harness.calls).toHaveLength(0);
    expect(lastStatus(harness)).toEqual({
      message: "Saving views is unavailable.",
      kind: "is-error",
    });
  });

  it("refuses a save with no name before any round trip", async () => {
    const harness = startSaver({});

    await harness.saver.save(focusInput({ name: "" }));

    expect(harness.calls).toHaveLength(0);
    expect(lastStatus(harness)?.kind).toBe("is-error");
  });
});
