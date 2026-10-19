import { describe, expect, it, vi } from "vitest";

import {
  buildSynthesisApplyForm,
  buildSynthesisContextFetchRequest,
  buildSynthesisContextForm,
  buildSynthesisGenerateForm,
  buildSynthesisPreviewForm,
  normalizeSynthesisApplyResponse,
  normalizeSynthesisContextResponse,
  normalizeSynthesisGenerateResponse,
  normalizeSynthesisManifest,
  normalizeSynthesisPreviewResponse,
  requestSynthesisApply,
  requestSynthesisContext,
  requestSynthesisGenerate,
  requestSynthesisPreview,
  synthesisApplyPath,
  synthesisContextPath,
  synthesisGeneratePath,
  synthesisPreviewPath,
  SynthesisRequestError,
} from "./api";
import {
  buildSynthesisSearchURL,
  requestSynthesisSearch,
  requestSynthesisSuggestions,
} from "./synthesize-requests";
import { jsonResponse } from "./shared.test-helpers";

const identity = (key: string): Record<string, unknown> => ({
  key,
  title: `${key} title`,
  path: `Notes/${key}.md`,
  route: `/n/${key}`,
  folder: "Notes",
});

const contextPayload = (overrides: Record<string, unknown> = {}): Record<string, unknown> => ({
  ok: true,
  context_id: "ctx-1",
  expires_at: "2026-08-19T12:00:00Z",
  vault_id: "vault-1",
  agent_enabled: false,
  purpose: "synthesis",
  prompt: "  keep spacing  ",
  anchor: identity("anchor"),
  supporters: [identity("one"), identity("two")],
  manifest: {
    source_path: "Notes/anchor.md",
    max_bytes: 40960,
    byte_count: 2048,
    notes: [identity("anchor"), identity("one"), identity("two")],
    excerpts: [
      { path: "Notes/one.md", start_line: 4, end_line: 9 },
      { path: "Notes/two.md", start_line: 12, end_line: 12 },
    ],
  },
  markdown: "# Packet\n\nbody\n",
  ...overrides,
});

describe("synthesis context form", () => {
  it("preserves candidate order and appends the token last", () => {
    const form = buildSynthesisContextForm({
      source: "anchor",
      candidates: ["second", "first", "third"],
      purpose: "contradictions",
      prompt: "why do these disagree",
      token: "csrf-token",
    });
    expect(Array.from(form.entries())).toEqual([
      ["source", "anchor"],
      ["candidate", "second"],
      ["candidate", "first"],
      ["candidate", "third"],
      ["purpose", "contradictions"],
      ["prompt", "why do these disagree"],
      ["token", "csrf-token"],
    ]);
  });

  it("omits an empty purpose and prompt so the server default applies", () => {
    const form = buildSynthesisContextForm({
      source: "anchor",
      candidates: [],
      purpose: "",
      prompt: "",
      token: "csrf-token",
    });
    expect(Array.from(form.keys())).toEqual(["source", "token"]);
  });

  it("posts form-encoded bytes with the CSRF header to the default path", () => {
    const request = buildSynthesisContextFetchRequest("", {
      source: "anchor",
      candidates: ["one"],
      purpose: "questions",
      prompt: "",
      token: "csrf-token",
    });
    expect(request.url).toBe(synthesisContextPath);
    expect(request.init.method).toBe("POST");
    expect(request.init.headers).toMatchObject({
      "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
      "X-BN-CSRF": "csrf-token",
    });
    expect(String(request.init.body)).toBe(
      "source=anchor&candidate=one&purpose=questions&token=csrf-token"
    );
  });
});

describe("synthesis context normalization", () => {
  it("keeps the packet Markdown byte-for-byte", () => {
    const normalized = normalizeSynthesisContextResponse(
      contextPayload({ markdown: "  leading and trailing  \n\n" })
    );
    expect(normalized?.markdown).toBe("  leading and trailing  \n\n");
  });

  it("drops identities and excerpts that cannot be addressed", () => {
    const manifest = normalizeSynthesisManifest({
      source_path: "Notes/anchor.md",
      max_bytes: "40960",
      byte_count: 2048,
      notes: [identity("one"), { key: "", path: "", route: "" }, "not-an-object"],
      excerpts: [{ path: "", start_line: 1, end_line: 2 }, { path: "Notes/one.md" }],
    });
    expect(manifest.notes.map((note) => note.key)).toEqual(["one"]);
    expect(manifest.excerpts).toEqual([{ path: "Notes/one.md", start_line: 0, end_line: 0 }]);
    expect(manifest.max_bytes).toBe(40960);
  });

  it("refuses a payload with no context identifier or anchor", () => {
    expect(normalizeSynthesisContextResponse(contextPayload({ context_id: "" }))).toBeNull();
    expect(normalizeSynthesisContextResponse(contextPayload({ anchor: null }))).toBeNull();
    expect(normalizeSynthesisContextResponse("nope")).toBeNull();
  });
});

describe("requestSynthesisContext", () => {
  it("returns the normalized packet when the response matches the request", async () => {
    const fetcher = vi.fn(async () => jsonResponse(contextPayload()));
    const response = await requestSynthesisContext(fetcher, "/api/synthesis/context", {
      source: "anchor",
      candidates: ["one", "two"],
      purpose: "synthesis",
      prompt: "",
      token: "csrf-token",
    });
    expect(response.context_id).toBe("ctx-1");
    expect(response.supporters.map((note) => note.key)).toEqual(["one", "two"]);
  });

  it("surfaces the server message on an error response", async () => {
    const fetcher = vi.fn(async () =>
      jsonResponse({ ok: false, code: "conflict", message: "A source changed on disk." }, 409)
    );
    await expect(
      requestSynthesisContext(fetcher, "/api/synthesis/context", {
        source: "anchor",
        candidates: [],
        purpose: "synthesis",
        prompt: "",
        token: "csrf-token",
      })
    ).rejects.toThrow("A source changed on disk.");
  });

  it("refuses a packet built for a different anchor or selection", async () => {
    const wrongAnchor = vi.fn(async () =>
      jsonResponse(contextPayload({ anchor: identity("other") }))
    );
    await expect(
      requestSynthesisContext(wrongAnchor, "/api/synthesis/context", {
        source: "anchor",
        candidates: ["one", "two"],
        purpose: "synthesis",
        prompt: "",
        token: "csrf-token",
      })
    ).rejects.toThrow("did not match the request");

    const wrongCount = vi.fn(async () =>
      jsonResponse(contextPayload({ supporters: [identity("one")] }))
    );
    await expect(
      requestSynthesisContext(wrongCount, "/api/synthesis/context", {
        source: "anchor",
        candidates: ["one", "two"],
        purpose: "synthesis",
        prompt: "",
        token: "csrf-token",
      })
    ).rejects.toThrow("did not match the request");
  });
});

describe("synthesis generate transport", () => {
  it("sends only the identifiers each write step is entitled to", () => {
    expect(
      Array.from(buildSynthesisGenerateForm({ context_id: "ctx-1", token: "csrf-token" }).entries())
    ).toEqual([
      ["context_id", "ctx-1"],
      ["token", "csrf-token"],
    ]);
    expect(
      Array.from(buildSynthesisApplyForm({ preview_id: "prev-1", token: "csrf-token" }).entries())
    ).toEqual([
      ["preview_id", "prev-1"],
      ["token", "csrf-token"],
    ]);
  });

  it("reads a disabled agent as a successful answer carrying a message", async () => {
    const normalized = normalizeSynthesisGenerateResponse({
      ok: true,
      enabled: false,
      context_id: "ctx-1",
      message: "Generation is unavailable until [agent].command is configured.",
    });
    expect(normalized.enabled).toBe(false);
    expect(normalized.output).toBe("");

    const fetcher = vi.fn<typeof fetch>(async () =>
      jsonResponse({ ok: true, enabled: false, context_id: "ctx-1", message: "no agent" })
    );
    const response = await requestSynthesisGenerate(fetcher, "", {
      context_id: "ctx-1",
      token: "csrf-token",
    });
    expect(String(fetcher.mock.calls[0][0])).toBe(synthesisGeneratePath);
    expect(response.message).toBe("no agent");
  });

  it("keeps the candidate bytes and refuses an enabled run with no output", async () => {
    const fetcher = vi.fn(async () =>
      jsonResponse({ ok: true, enabled: true, context_id: "ctx-1", output: "  spaced draft  \n" })
    );
    const response = await requestSynthesisGenerate(fetcher, synthesisGeneratePath, {
      context_id: "ctx-1",
      token: "csrf-token",
    });
    expect(response.output).toBe("  spaced draft  \n");

    const empty = vi.fn(async () => jsonResponse({ ok: true, enabled: true, output: "" }));
    await expect(
      requestSynthesisGenerate(empty, synthesisGeneratePath, {
        context_id: "ctx-1",
        token: "csrf-token",
      })
    ).rejects.toThrow("returned no output");
  });
});

describe("synthesis preview and apply transport", () => {
  it("sends the draft exactly as typed so the preview describes the same bytes", () => {
    const form = buildSynthesisPreviewForm({
      context_id: "ctx-1",
      title: "Result",
      folder: "Synthesis",
      draft: "  spaced\r\n\r\nlines  \n",
      token: "csrf-token",
    });
    expect(form.get("draft")).toBe("  spaced\r\n\r\nlines  \n");
    expect(Array.from(form.keys())).toEqual([
      "context_id",
      "title",
      "folder",
      "draft",
      "token",
    ]);
  });

  it("refuses a preview with no receipt or no destination", async () => {
    expect(normalizeSynthesisPreviewResponse({ ok: true, preview_id: "", destination: "a.md" })).toBeNull();
    expect(normalizeSynthesisPreviewResponse({ ok: true, preview_id: "p", destination: "" })).toBeNull();
    expect(normalizeSynthesisPreviewResponse(42)).toBeNull();

    const fetcher = vi.fn<typeof fetch>(async () =>
      jsonResponse({
        ok: true,
        preview_id: "prev-1",
        expires_at: "2026-08-19T12:05:00Z",
        context_id: "ctx-1",
        destination: "Synthesis/Result.md",
        markdown: "---\ntitle: Result\n---\n",
        rendered_html: "<p>body</p>",
      })
    );
    const response = await requestSynthesisPreview(fetcher, "", {
      context_id: "ctx-1",
      title: "Result",
      folder: "Synthesis",
      draft: "body",
      token: "csrf-token",
    });
    expect(String(fetcher.mock.calls[0][0])).toBe(synthesisPreviewPath);
    expect(response.markdown).toBe("---\ntitle: Result\n---\n");
  });

  it("requires the apply response to name the created note", async () => {
    expect(normalizeSynthesisApplyResponse({ ok: true, key: "Result" })).toBeNull();
    expect(normalizeSynthesisApplyResponse({ ok: false, code: "conflict" })).toBeNull();
    expect(
      normalizeSynthesisApplyResponse({ ok: true, path: "/n/Result", rel_path: "Result.md" })
    ).toEqual({
      message: "Created note.",
      key: "",
      path: "/n/Result",
      rel_path: "Result.md",
      journal_id: "",
    });

    const fetcher = vi.fn<typeof fetch>(async () =>
      jsonResponse({ ok: true, message: "Created note.", path: "/n/Result", journal_id: "j-1" })
    );
    const response = await requestSynthesisApply(fetcher, "", {
      preview_id: "prev-1",
      token: "csrf-token",
    });
    expect(String(fetcher.mock.calls[0][0])).toBe(synthesisApplyPath);
    expect(response.journal_id).toBe("j-1");
  });

  it("carries the management error code beside the message", async () => {
    const fetcher = vi.fn(async () =>
      jsonResponse({ ok: false, code: "not_found", message: "context expired" }, 404)
    );
    const error = await requestSynthesisApply(fetcher, synthesisApplyPath, {
      preview_id: "prev-1",
      token: "csrf-token",
    }).catch((thrown: unknown) => thrown);
    expect(error).toBeInstanceOf(SynthesisRequestError);
    expect((error as SynthesisRequestError).code).toBe("not_found");
    expect((error as SynthesisRequestError).message).toBe("context expired");
  });
});

describe("synthesis suggestion and search requests", () => {
  it("asks the Related endpoint for a fixed suggestion limit", async () => {
    const fetcher = vi.fn<typeof fetch>(async () =>
      jsonResponse({
        server_date: "2026-08-19",
        source: identity("anchor"),
        mode: "rediscover",
        has_more: false,
        results: [],
        feedback: [],
      })
    );
    await requestSynthesisSuggestions(fetcher, "/api/related", "anchor", "rediscover");
    const url = new URL(String(fetcher.mock.calls[0][0]), "https://example.test");
    expect(url.pathname).toBe("/api/related");
    expect(url.searchParams.get("source")).toBe("anchor");
    expect(url.searchParams.get("mode")).toBe("rediscover");
    expect(url.searchParams.get("limit")).toBe("8");
  });

  it("encodes the search query and normalizes the result rows", async () => {
    expect(buildSynthesisSearchURL("/api/search", "two words")).toBe("/api/search?q=two+words");
    const fetcher = vi.fn(async () =>
      jsonResponse([
        { key: "one", path: "/n/one", title: "One", snippet: "<mark>one</mark>" },
        { key: "", path: "/n/", title: "" },
      ])
    );
    const results = await requestSynthesisSearch(fetcher, "/api/search", "one");
    expect(results).toHaveLength(2);
    expect(results[0].snippet).toBe("<mark>one</mark>");
  });

  it("reports a failed search with the response status", async () => {
    const fetcher = vi.fn(async () => new Response("search error", { status: 500 }));
    await expect(requestSynthesisSearch(fetcher, "/api/search", "one")).rejects.toThrow(
      "search error"
    );
  });
});
