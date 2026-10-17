import { unansweredRequest } from "./request-outcome";
import { describe, expect, it, vi } from "vitest";

import {
  buildHistoryPreviewForm,
  buildHistoryQuery,
  normalizeHistoryPreview,
  normalizeHistoryResult,
} from "./api";
import {
  conflicted,
  fetchHistoryPage,
  previewHistoryRestore,
  restoreHistoryEntry,
  stalePreview,
  type HistoryFetch,
} from "./history-requests";

const jsonResponse = (body: unknown, status = 200): Response =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

describe("history query building", () => {
  it("omits what it has nothing to say about and repeats what the route reads", () => {
    expect(buildHistoryQuery({}).toString()).toBe("");
    expect(
      buildHistoryQuery({
        limit: 50,
        offset: 100,
        origins: ["browser", " import ", "  "],
        classes: ["note"],
        path: "  Inbox/  ",
      }).toString()
    ).toBe("limit=50&offset=100&origin=browser&origin=import&class=note&path=Inbox%2F");
  });

  it("drops a limit or offset that is not a usable number", () => {
    expect(buildHistoryQuery({ limit: 0, offset: -1 }).toString()).toBe("");
    expect(buildHistoryQuery({ limit: Number.NaN }).toString()).toBe("");
    expect(buildHistoryQuery({ limit: 20.7 }).toString()).toBe("limit=20");
  });

  it("sends a preview request with no entry when the latest one is meant", () => {
    expect(buildHistoryPreviewForm({ token: "t" }).toString()).toBe("token=t");
    expect(buildHistoryPreviewForm({ token: "t", entryID: " e " }).toString()).toBe("token=t&id=e");
  });
});

describe("history page normalization", () => {
  it("reads the window, the vocabulary and the warnings the server published", () => {
    const result = normalizeHistoryResult({
      ok: true,
      entries: [],
      total: "51",
      offset: "20",
      limit: "20",
      has_more: true,
      origins: [{ value: " browser ", label: " Browser " }, { value: "" }, "nope"],
      classes: [{ value: "note" }],
      warnings: [{ id: "e1", message: "unreadable" }],
      warning_count: "9",
    });

    expect(result.total).toBe(51);
    expect(result.offset).toBe(20);
    expect(result.has_more).toBe(true);
    expect(result.origins).toEqual([{ value: "browser", label: "Browser" }]);
    expect(result.classes).toEqual([{ value: "note", label: "note" }]);
    expect(result.warning_count).toBe(9);
  });

  it("never reports fewer unreadable entries than it was handed", () => {
    const result = normalizeHistoryResult({
      ok: true,
      warnings: [{ id: "e1", message: "a" }, { id: "e2", message: "b" }],
      warning_count: 0,
    });
    expect(result.warning_count).toBe(2);
  });
});

describe("restore preview normalization", () => {
  it("treats a reply with no receipt as a refusal, however cheerful it is", () => {
    const result = normalizeHistoryPreview({ ok: true, entry_id: "e1", changes: [] });
    // Read through the discriminant rather than around it: a reply that stayed
    // ok would otherwise skip the assertion that matters here.
    expect(result.ok ? "" : result.code).toBe("preview_required");
  });

  it("carries the receipt, the durability of each path, and what is contested", () => {
    const result = normalizeHistoryPreview({
      ok: true,
      preview_id: "p1",
      entry_id: "e1",
      requires_force: true,
      changes: [{
        path: "A.md",
        current_state: "file",
        restored_state: "missing",
        class: "note",
        recorded: true,
        travels: false,
        conflict: true,
      }],
      conflicts: [{ path: "A.md", expected_state: "missing", current_state: "file" }],
    });

    expect(result.preview_id).toBe("p1");
    expect(result.requires_force).toBe(true);
    expect(result.changes).toEqual([{
      path: "A.md",
      current_state: "file",
      restored_state: "missing",
      class: "note",
      recorded: true,
      travels: false,
      conflict: true,
    }]);
    expect(result.conflicts).toHaveLength(1);
  });
});

describe("history requests", () => {
  const fetcherFor = (resp: Response | Error): HistoryFetch =>
    vi.fn(async () => {
      if (resp instanceof Error) {
        throw resp;
      }
      return resp;
    });

  it("reads a page without a token, because reading is not a write", async () => {
    const fetcher = vi.fn(async () => jsonResponse({ ok: true, entries: [] }));
    const outcome = await fetchHistoryPage(fetcher, "/api/history", { limit: 20 });
    expect(outcome.ok).toBe(true);
    expect(fetcher).toHaveBeenCalledWith("/api/history?limit=20", {
      headers: { Accept: "application/json" },
    });
  });

  it("keeps the server's typed code beside the message", async () => {
    const outcome = await previewHistoryRestore(
      fetcherFor(jsonResponse({ ok: false, code: "preview_stale", message: "It moved." }, 409)),
      "/api/history/preview",
      "t",
      "e1"
    );
    expect(outcome.ok).toBe(false);
    if (outcome.ok) {
      throw new Error("expected a refusal");
    }
    expect(outcome.code).toBe("preview_stale");
    expect(outcome.status).toBe(409);
    expect(stalePreview(outcome)).toBe(true);
    expect(conflicted(outcome)).toBe(false);
  });

  it("reports a refused restore as a conflict carrying the paths that refused", async () => {
    const outcome = await restoreHistoryEntry(
      fetcherFor(jsonResponse({
        ok: false,
        code: "conflict",
        message: "Undo has conflicts.",
        requires_force: true,
        conflicts: [{ path: "A.md", expected_state: "missing", current_state: "file" }],
      }, 409)),
      "/api/undo",
      { token: "t", entryID: "e1" }
    );
    expect(outcome.ok).toBe(false);
    if (outcome.ok) {
      throw new Error("expected a refusal");
    }
    expect(conflicted(outcome)).toBe(true);
    expect(outcome.conflicts).toEqual([
      { path: "A.md", expected_state: "missing", current_state: "file" },
    ]);
  });

  it("says the server did not answer rather than inventing a reason", async () => {
    const outcome = await restoreHistoryEntry(
      fetcherFor(new Error("offline")),
      "/api/undo",
      { token: "t" }
    );
    expect(outcome.ok).toBe(false);
    if (outcome.ok) {
      throw new Error("expected a refusal");
    }
    expect(outcome.status).toBe(0);
    expect(outcome.message).toBe(unansweredRequest("write").message);
    expect(outcome.conflicts).toEqual([]);
  });

  it("posts a restore as a form with the token in the header the guard reads", async () => {
    const fetcher = vi.fn(async () => jsonResponse({ ok: true, message: "done" }));
    await restoreHistoryEntry(fetcher, "/api/undo", {
      token: "csrf-token",
      entryID: "e1",
      force: true,
      confirmForce: true,
      previewID: "p1",
    });
    expect(fetcher).toHaveBeenCalledWith("/api/undo", {
      method: "POST",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
        "X-BN-CSRF": "csrf-token",
      },
      body: "token=csrf-token&id=e1&force=1&confirm_force=1&preview_id=p1",
    });
  });
});
