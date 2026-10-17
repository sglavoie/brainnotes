import { unansweredRequest } from "./request-outcome";
import { expect, it, vi } from "vitest";
import {
  applySyncResolution,
  fetchSyncConflicts,
  fetchSyncStatus,
  previewSyncResolution,
  stalePreview,
  type FetchLike,
} from "./sync-requests";
import { syncJSON, syncOperation, syncStatus } from "./sync.test-helpers";

// The transport for the three synchronization routes. What it has to get right is
// the *code* beside the message: a rejected resolution is either "the vault moved
// on, decide again, your draft is still yours" or "this vault will not write right
// now", and a page that had to tell those apart from English would guess.

const answering = (...responses: Response[]): { fetch: FetchLike; calls: () => unknown[][] } => {
  const queue = [...responses];
  const fetcher = vi.fn<FetchLike>(async () => {
    const next = queue.shift();
    if (!next) {
      throw new Error("no response left for this request");
    }
    return next;
  });
  return { fetch: fetcher, calls: () => fetcher.mock.calls };
};

const bodyOf = (init: RequestInit | undefined): Record<string, unknown> =>
  JSON.parse(String(init?.body ?? "null")) as Record<string, unknown>;

it("reads the status and the holds through their own normalizers", async () => {
  const status = await fetchSyncStatus(
    answering(syncJSON(syncStatus({ summary: "1 change is waiting for a decision." }))).fetch
  );
  expect(status.ok && status.result.summary).toBe("1 change is waiting for a decision.");

  const conflicts = await fetchSyncConflicts(
    answering(syncJSON({ ok: true, operations: [syncOperation()] })).fetch
  );
  expect(conflicts.ok && conflicts.result.operations[0].package_id).toBe("pkg-1");
});

it("carries the server's flat refusal envelope as a code and a message", async () => {
  const outcome = await applySyncResolution(
    answering(
      syncJSON(
        {
          ok: false,
          code: "preview_stale",
          message: "this vault moved on while the decision was being made",
        },
        409
      )
    ).fetch,
    "/api/sync/conflicts/resolve",
    "csrf-token",
    "preview-1"
  );

  expect(outcome.ok).toBe(false);
  if (outcome.ok) {
    throw new Error("a 409 was reported as a success");
  }
  expect(outcome.code).toBe("preview_stale");
  expect(outcome.message).toBe("this vault moved on while the decision was being made");
  expect(stalePreview(outcome)).toBe(true);
});

it("names both re-read refusals and nothing else as stale", () => {
  expect(stalePreview({ ok: false, code: "preview_stale", message: "" })).toBe(true);
  // A missing or spent receipt is the same advice: read both versions and decide
  // again. A safety refusal is not — deciding again is the wrong thing to suggest.
  expect(stalePreview({ ok: false, code: "preview_required", message: "" })).toBe(true);
  expect(stalePreview({ ok: false, code: "safety_error", message: "" })).toBe(false);
  expect(stalePreview({ ok: false, code: "", message: "" })).toBe(false);
});

it("treats a 200 that says ok:false as the refusal it is", async () => {
  const outcome = await fetchSyncStatus(
    answering(syncJSON({ ok: false, code: "forbidden", message: "read-only replica" })).fetch
  );

  expect(outcome).toEqual({ ok: false, code: "forbidden", message: "read-only replica" });
});

it("falls back to the status when the refusal carries no sentence", async () => {
  const outcome = await fetchSyncConflicts(answering(syncJSON({ ok: false }, 500)).fetch);

  expect(outcome).toEqual({ ok: false, code: "", message: "Request failed (500)." });
});

it("says the server did not answer rather than throwing at the page", async () => {
  const fetcher: FetchLike = () => Promise.reject(new Error("socket closed"));

  expect(await fetchSyncStatus(fetcher)).toEqual({
    ok: false,
    code: "",
    message: "The server did not answer.",
  });
  expect(await applySyncResolution(fetcher, "", "csrf-token", "preview-1")).toEqual({
    ...unansweredRequest("write"),
    code: "unconfirmed",
  });
});

it("sends every decision in one preview request, with the token in both places", async () => {
  const server = answering(syncJSON({ ok: true, preview_id: "preview-1" }));

  await previewSyncResolution(server.fetch, "", "csrf-token", "pkg-1", [
    { path: "Alpha.md", choice: "keep-current", merged: null },
    { path: "Beta.md", choice: "manual-merge", merged: "composed\n" },
  ]);

  const [endpoint, init] = server.calls()[0] as [string, RequestInit];
  expect(endpoint).toBe("/api/sync/conflicts/preview");
  expect((init.headers as Record<string, string>)["X-BN-CSRF"]).toBe("csrf-token");
  expect(bodyOf(init)).toEqual({
    token: "csrf-token",
    package_id: "pkg-1",
    paths: [
      { path: "Alpha.md", choice: "keep-current", merged: null },
      { path: "Beta.md", choice: "manual-merge", merged: "composed\n" },
    ],
  });
});

it("redeems a receipt and nothing else, so the plan the server holds is the one applied", async () => {
  const server = answering(syncJSON({ ok: true, journal_id: "entry-1", acknowledged: true }));

  const outcome = await applySyncResolution(server.fetch, "", "csrf-token", "preview-1");

  const [endpoint, init] = server.calls()[0] as [string, RequestInit];
  expect(endpoint).toBe("/api/sync/conflicts/resolve");
  // The decision itself is not re-sent: it is the receipt's, and re-sending it
  // would be a second chance to disagree with what was reviewed.
  expect(bodyOf(init)).toEqual({ preview_id: "preview-1" });
  expect(outcome.ok && outcome.result.acknowledged).toBe(true);
});

it("reads the status route without a token, because it writes nothing", async () => {
  const server = answering(syncJSON(syncStatus()));

  await fetchSyncStatus(server.fetch, "/api/sync/status");

  const [, init] = server.calls()[0] as [string, RequestInit];
  expect(init.method ?? "GET").toBe("GET");
  expect(init.headers).toEqual({ Accept: "application/json" });
});
