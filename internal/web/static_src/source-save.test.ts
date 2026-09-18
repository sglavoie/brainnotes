import { describe, expect, it, vi } from "vitest";

import { normalizeSourceDocument, normalizeSourceSaveResponse, sourceSaveConflict } from "./api";
import { classifySaveResponse, createSourceClient, newSaveID } from "./source-save";

const jsonResponse = (status: number, payload: unknown): Response =>
  new Response(JSON.stringify(payload), {
    status,
    headers: { "Content-Type": "application/json" },
  });

const client = (fetcher: typeof fetch) =>
  createSourceClient({
    endpoints: {
      read: "/api/source",
      preview: "/api/source/preview",
      save: "/api/source/save",
      search: "/api/search",
    },
    key: "notes/alpha",
    token: "csrf-token",
    fetch: fetcher,
  });

describe("normalizeSourceDocument", () => {
  // The document's content is the exact bytes the revision hashes to. Trimming
  // it — which every other string on the wire gets — would make the editor's
  // first save of an untouched note a rewrite.
  it("keeps the source byte-for-byte, including the trailing newline", () => {
    const doc = normalizeSourceDocument({
      key: "alpha",
      path: "/n/alpha",
      rel_path: "Alpha.md",
      revision: "abc123",
      content: "---\ntitle: Alpha\n---\n\nBody.\n",
    });
    expect(doc.content).toBe("---\ntitle: Alpha\n---\n\nBody.\n");
    expect(doc.revision).toBe("abc123");
  });

  it("answers an unusable payload with empty fields rather than throwing", () => {
    expect(normalizeSourceDocument(null).content).toBe("");
    expect(normalizeSourceDocument({ content: 12 }).content).toBe("");
  });
});

// BASE is the revision the editor read the note at, and so the one every save
// below is planned against. It is an argument rather than a constant inside the
// classifier because one of the four outcomes is not a property of the response
// alone — see sourceSaveConflict.
const BASE = "r1";

describe("classifySaveResponse", () => {
  it("reads a write as saved and carries the revision forward", () => {
    const outcome = classifySaveResponse(
      normalizeSourceSaveResponse({ ok: true, revision: "r2", message: "Saved Alpha.md.", changed: true }),
      BASE
    );
    expect(outcome).toEqual({ kind: "saved", revision: "r2", message: "Saved Alpha.md." });
  });

  it("reads a replay as a success, wording and all", () => {
    const outcome = classifySaveResponse(
      normalizeSourceSaveResponse({
        ok: true,
        revision: "r2",
        replayed: true,
        message: "Alpha.md was already saved with this save id.",
      }),
      BASE
    );
    expect(outcome.kind).toBe("saved");
    expect(outcome).toHaveProperty("message", "Alpha.md was already saved with this save id.");
  });

  // The snapshot failure. The bytes are on disk, so the revision has to move:
  // a client that resubmitted against the old one would be planning a save
  // against a revision its own write replaced.
  it("reads a snapshot failure as landed, with the new revision", () => {
    const outcome = classifySaveResponse(
      normalizeSourceSaveResponse({
        ok: false,
        code: "snapshot_failed",
        message: "the change is on disk and further writes are blocked",
        revision: "r3",
        changed: true,
      }),
      BASE
    );
    expect(outcome).toEqual({
      kind: "landed",
      revision: "r3",
      message: "the change is on disk and further writes are blocked",
    });
  });

  it("falls back to a sentence when the server sent no message", () => {
    expect(
      classifySaveResponse(normalizeSourceSaveResponse({ ok: false, code: "invalid_request" }), BASE)
    ).toEqual({
      kind: "failed",
      answered: true,
      message: "The note could not be saved.",
    });
  });

  // A body with no code at all was not this API's answer — a proxy's own error
  // page, a truncated response. Nothing here knows whether the bytes landed, so
  // `answered` is false and the caller keeps the attempt's save_id: the ledger
  // is what turns the retry into a replay rather than a second write.
  it("reads a body that is not this envelope as an unknown outcome", () => {
    const outcome = classifySaveResponse(normalizeSourceSaveResponse({ ok: false }), BASE);

    expect(outcome.kind).toBe("failed");
    expect(outcome).toMatchObject({ answered: false });
    expect(outcome.message).toContain("whether the note was written is unknown");
  });
});

// The conflict arm on its own, because it is the one outcome that is not a
// property of the response alone: the same code, the same fields, and a
// different answer depending on the revision the editor is holding.
describe("classifySaveResponse on a conflict", () => {
  it("reads a stale revision as a conflict with the bytes in the way", () => {
    const outcome = classifySaveResponse(
      normalizeSourceSaveResponse({
        ok: false,
        code: "conflict",
        message: "source: Alpha.md changed since it was loaded, so nothing was written",
        revision: "r9",
        content: "Somebody else's text.\n",
      }),
      BASE
    );
    expect(outcome).toEqual({
      kind: "conflict",
      revision: "r9",
      content: "Somebody else's text.\n",
      message: "source: Alpha.md changed since it was loaded, so nothing was written",
    });
  });

  // The configuration pin moving at the commit boundary. classifySourceSaveTxnError
  // turns every transaction conflict into a revision error and the handler
  // re-reads the note for all of them, so this refusal arrives looking exactly
  // like a stale one — except that the version it carries is the version the
  // editor already has. Offering a merge here would ask somebody to choose
  // between their draft and their own starting text.
  //
  // The message is the server's verbatim, and it is the reason this outcome
  // gets a sentence of its own: newSourceRevisionError writes "changed since it
  // was loaded" for every precondition, which is false for the one precondition
  // that is not about the note.
  it("reads a conflict carrying the revision already in hand as a plain failure", () => {
    const outcome = classifySaveResponse(
      normalizeSourceSaveResponse({
        ok: false,
        code: "conflict",
        message: "source: Alpha.md changed since it was loaded, so nothing was written",
        revision: BASE,
        content: "Alpha body.\n",
      }),
      BASE
    );
    expect(outcome).toMatchObject({ kind: "failed", answered: true });
    expect(outcome.message).not.toContain("changed since it was loaded");
    expect(outcome.message).toContain("nothing was written");
    expect(outcome.message).toContain("press Save to try again");
  });

  // The save-id reuse refusal (planSourceSaveLedger). It is raised before any
  // plan exists, so it is the one `conflict` that carries no version at all —
  // and there is correspondingly nothing to merge against.
  it("reads a conflict with no version as a plain failure", () => {
    const outcome = classifySaveResponse(
      normalizeSourceSaveResponse({
        ok: false,
        code: "conflict",
        message: 'source: save id "s-1" already recorded a different save of Alpha.md',
      }),
      BASE
    );
    expect(outcome).toEqual({
      kind: "failed",
      answered: true,
      message: 'source: save id "s-1" already recorded a different save of Alpha.md',
    });
  });
});

describe("sourceSaveConflict", () => {
  it("is null for a success", () => {
    expect(sourceSaveConflict(normalizeSourceSaveResponse({ ok: true }), BASE)).toBeNull();
  });

  // The discriminator is the revision having moved, not the content. The server
  // re-reads the note for every conflict, and a note somebody just emptied
  // re-reads as zero bytes — `content` is omitempty on the wire, so keying on it
  // would turn "somebody emptied this note" into a refusal with no panel and no
  // re-pin, and every later save would meet the same 409 with no way out.
  it("offers the merge for a note the other version emptied", () => {
    expect(
      sourceSaveConflict(
        normalizeSourceSaveResponse({ ok: false, code: "conflict", revision: "r9" }),
        BASE
      )
    ).toEqual({ revision: "r9", content: "" });
  });

  // A `conflict` carrying the base revision is a precondition that failed
  // somewhere other than the note — the configuration pin. Nothing moved under
  // the edit, so there is no other version to show, whatever bytes the re-read
  // attached.
  it("is null for a conflict over the version the editor already holds", () => {
    expect(
      sourceSaveConflict(
        normalizeSourceSaveResponse({ ok: false, code: "conflict", revision: BASE, content: "Alpha body.\n" }),
        BASE
      )
    ).toBeNull();
  });

  // The save-id reuse refusal, which is decided before the note is ever read.
  it("is null for a conflict the server offered no version for", () => {
    expect(
      sourceSaveConflict(
        normalizeSourceSaveResponse({ ok: false, code: "conflict", content: "stale\n" }),
        BASE
      )
    ).toBeNull();
  });
});

describe("createSourceClient", () => {
  it("reads a note's source by key and reports a refusal as an error", async () => {
    const fetcher = vi.fn().mockResolvedValue(
      jsonResponse(200, { ok: true, revision: "r1", content: "Body.\n", rel_path: "Alpha.md" })
    );
    const doc = await client(fetcher as unknown as typeof fetch).read();

    expect(fetcher).toHaveBeenCalledWith("/api/source?key=notes%2Falpha", expect.anything());
    expect(doc).toMatchObject({ revision: "r1", content: "Body.\n", rel_path: "Alpha.md" });

    const refusing = vi.fn().mockResolvedValue(jsonResponse(403, { ok: false, message: "read-only replica" }));
    await expect(client(refusing as unknown as typeof fetch).read()).rejects.toThrow("read-only replica");
  });

  it("posts a save as a form carrying the revision, the save id and the token", async () => {
    const fetcher = vi.fn().mockResolvedValue(jsonResponse(200, { ok: true, revision: "r2" }));
    await client(fetcher as unknown as typeof fetch).save({
      content: "new body\n",
      revision: "r1",
      saveID: "save-1",
    });

    const [url, init] = fetcher.mock.calls[0];
    expect(url).toBe("/api/source/save");
    expect(init.headers["X-BN-CSRF"]).toBe("csrf-token");
    const body = new URLSearchParams(init.body as string);
    expect(body.get("key")).toBe("notes/alpha");
    expect(body.get("content")).toBe("new body\n");
    expect(body.get("revision")).toBe("r1");
    expect(body.get("save_id")).toBe("save-1");
    expect(body.get("token")).toBe("csrf-token");
  });

  // Every arm of the save envelope is a fact the editor acts on. Throwing on a
  // refusal would flatten "the note changed" and "the bytes landed but the
  // history does not cover them" into one indistinguishable failure.
  it("returns a refused save rather than throwing", async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValue(jsonResponse(409, { ok: false, code: "conflict", content: "theirs", revision: "r9" }));
    const response = await client(fetcher as unknown as typeof fetch).save({
      content: "mine",
      revision: "r1",
      saveID: "save-1",
    });
    expect(response.ok).toBe(false);
    expect(classifySaveResponse(response, "r1").kind).toBe("conflict");
  });

  it("reports a preview refusal as an error and an empty render as empty", async () => {
    const failing = vi
      .fn()
      .mockResolvedValue(jsonResponse(400, { ok: false, message: "source: frontmatter is not valid YAML" }));
    await expect(client(failing as unknown as typeof fetch).preview("bad")).rejects.toThrow(
      "frontmatter is not valid YAML"
    );

    const empty = vi.fn().mockResolvedValue(jsonResponse(200, { ok: true, rendered_html: "" }));
    await expect(client(empty as unknown as typeof fetch).preview("")).resolves.toBe("");
  });

  it("treats a refused search as no completions rather than an error", async () => {
    const fetcher = vi.fn().mockResolvedValue(jsonResponse(500, { ok: false }));
    await expect(client(fetcher as unknown as typeof fetch).search("/api/search?q=a")).resolves.toEqual([]);
  });
});

describe("newSaveID", () => {
  it("mints a distinct id per call", () => {
    expect(newSaveID()).not.toBe(newSaveID());
    expect(newSaveID()).not.toBe("");
  });
});
