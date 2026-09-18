// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import {
  buildBulkApplyForm,
  buildBulkNotesForm,
  buildBulkPreviewForm,
  normalizeBulkFrontmatterChanges,
  normalizeBulkNoteKeys,
  normalizeBulkManagementResult,
  normalizeBulkPreviewManagementResult,
  normalizeBulkPreviewValue,
  normalizeManagementErrorMessage,
  normalizeSearchResults,
  searchResultPath,
} from "./api";
import { buildSearchAPIURL } from "./filters";
import { setURL } from "./search-test-helpers";

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  document.body.innerHTML = "";
  setURL("/");
});

describe("search URL and result contracts", () => {
  it("builds search API URLs with optional shared filters", () => {
    setURL("/?tag=projects&category=weekly&q=old&broken=true#top");

    expect(buildSearchAPIURL(" daily note ", true)).toBe(
      "/api/search?q=daily+note&tag=projects&category=weekly&broken=true"
    );
    expect(buildSearchAPIURL(" daily note ", false)).toBe("/api/search?q=daily+note");
    expect(buildSearchAPIURL("", false)).toBe("/api/search");
  });

  it("normalizes result payloads and resolves safe note paths", () => {
    const results = normalizeSearchResults([
      {
        key: " daily/index.md ",
        path: " /n/server/path ",
        title: " Daily Index ",
        wikilink: " daily/index ",
        folder: " daily ",
        matched_fields: [" title ", "", "body"],
        issues: ["dead_end", 3],
        snippet: " <mark>daily</mark> ",
        pinned: true,
      },
      {
        key: "folder/name with spaces.md",
      },
      "bad",
    ]);

    expect(results).toHaveLength(2);
    expect(results[0]).toEqual({
      key: "daily/index.md",
      path: "/n/server/path",
      title: "Daily Index",
      wikilink: "daily/index",
      folder: "daily",
      matched_fields: ["title", "body"],
      issues: ["dead_end", "3"],
      snippet: "<mark>daily</mark>",
      pinned: true,
    });
    expect(searchResultPath(results[0])).toBe("/n/server/path");
    expect(searchResultPath(results[1])).toBe("/n/folder/name%20with%20spaces.md");
  });
});

describe("bulk request bodies", () => {
  it("normalizes explicit selected keys for bulk preview and apply requests", () => {
    expect(normalizeBulkNoteKeys([" source ", "", "Source", "folder/Note"])).toEqual([
      "source",
      "folder/Note",
    ]);

    const previewBody = buildBulkPreviewForm({
      action: "move",
      keys: [" source ", "Source", "archive/a"],
      token: "csrf-token",
      folder: "Archive",
    });
    const applyBody = buildBulkApplyForm({
      action: "frontmatter_set",
      keys: [" source ", "Source", "archive/a"],
      token: "csrf-token",
      property: "status",
      op: "=",
      value: "done",
      previewID: " preview-123 ",
    });
    const compatBody = buildBulkNotesForm({
      action: "frontmatter_unset",
      keys: [" source ", "Source"],
      token: "csrf-token",
      property: "status",
      previewID: "preview-compat",
    });

    expect(previewBody.get("action")).toBe("move");
    expect(previewBody.get("token")).toBe("csrf-token");
    expect(previewBody.get("folder")).toBe("Archive");
    expect(previewBody.get("preview_id")).toBeNull();
    expect(previewBody.getAll("keys[]")).toEqual(["source", "archive/a"]);

    expect(applyBody.get("action")).toBe("frontmatter_set");
    expect(applyBody.get("property")).toBe("status");
    expect(applyBody.get("op")).toBe("=");
    expect(applyBody.get("value")).toBe("done");
    expect(applyBody.get("preview_id")).toBe("preview-123");
    expect(applyBody.getAll("keys[]")).toEqual(["source", "archive/a"]);

    expect(compatBody.get("preview_id")).toBe("preview-compat");
  });
});

// The bulk preview envelope, as the server spells it and as the page must read
// it. Both literals are long because the envelope is — every scalar arrives
// stringly typed and every string padded — so they sit at module scope and the
// test below reads as the single comparison it is.
const bulkPreviewPayload = {
  ok: true,
  action: " move ",
  message: " Preview: 1 note would be moved. 1 failed. ",
  changed: "1",
  unchanged: "0",
  failed: "1",
  preview_id: " preview-123 ",
  expires_at: "2026-06-02T12:34:56-04:00",
  results: [
    {
      key: " inbox/source ",
      path: " /n/archive/source ",
      rel_path: " Archive/Source.md ",
      source: {
        key: " inbox/source ",
        path: " /n/inbox/source ",
        rel_path: " Inbox/Source.md ",
      },
      destination: {
        key: " archive/source ",
        path: " /n/archive/source ",
        rel_path: " Archive/Source.md ",
      },
      changed: true,
      move: {
        rewrite_count: "2",
        rewrite_file_count: "2",
        body_rewrite_count: "1",
        frontmatter_rewrite_count: "1",
      },
      frontmatter: [
        {
          key: "status",
          before: { present: true, value: "draft" },
          after: { present: true, value: "done" },
          changed: 1,
        },
        {
          key: "empty_value",
          before: { present: true, value: "" },
          after: { present: false, value: "ignored" },
          changed: true,
        },
      ],
      affected: [
        {
          role: " source ",
          key: " inbox/source ",
          path: " /n/inbox/source ",
          rel_path: " Inbox/Source.md ",
          exists: 1,
        },
        {
          role: " destination ",
          key: " archive/source ",
          path: " /n/archive/source ",
          rel_path: " Archive/Source.md ",
          exists: false,
        },
      ],
    },
    "bad",
  ],
  failures: [
    { key: " missing ", rel_path: " Missing.md ", code: " not_found ", message: " note not found " },
    {},
  ],
};

const bulkPreviewNormalized = {
  ok: true,
  action: "move",
  message: "Preview: 1 note would be moved. 1 failed.",
  changed: 1,
  unchanged: 0,
  failed: 1,
  preview_id: "preview-123",
  expires_at: "2026-06-02T16:34:56.000Z",
  results: [
    {
      key: "inbox/source",
      path: "/n/archive/source",
      rel_path: "Archive/Source.md",
      source: {
        key: "inbox/source",
        path: "/n/inbox/source",
        rel_path: "Inbox/Source.md",
      },
      destination: {
        key: "archive/source",
        path: "/n/archive/source",
        rel_path: "Archive/Source.md",
      },
      changed: true,
      move: {
        rewrite_count: 2,
        rewrite_file_count: 2,
        body_rewrite_count: 1,
        frontmatter_rewrite_count: 1,
      },
      frontmatter: [
        {
          key: "status",
          before: { present: true, value: "draft" },
          after: { present: true, value: "done" },
          changed: true,
        },
        {
          key: "empty_value",
          before: { present: true, value: "" },
          after: { present: false },
          changed: true,
        },
      ],
      affected: [
        {
          role: "source",
          key: "inbox/source",
          path: "/n/inbox/source",
          rel_path: "Inbox/Source.md",
          exists: true,
        },
        {
          role: "destination",
          key: "archive/source",
          path: "/n/archive/source",
          rel_path: "Archive/Source.md",
          exists: false,
        },
      ],
    },
  ],
  failures: [
    {
      key: "missing",
      rel_path: "Missing.md",
      code: "not_found",
      message: "note not found",
    },
  ],
};

describe("bulk preview contracts", () => {
  it("normalizes bulk preview rows, frontmatter changes, failures, and expiry timestamps", () => {
    expect(normalizeBulkPreviewManagementResult(bulkPreviewPayload)).toEqual(bulkPreviewNormalized);

    expect(normalizeBulkPreviewValue({ present: false, value: "ignored" })).toEqual({
      present: false,
    });
    expect(normalizeBulkFrontmatterChanges([
      { key: "status", before: { present: false }, after: { present: true, value: "done" }, changed: "true" },
    ])).toEqual([
      {
        key: "status",
        before: { present: false },
        after: { present: true, value: "done" },
        changed: true,
      },
    ]);
  });
});

describe("bulk apply contracts", () => {
  it("normalizes bulk apply results and preserves preview error messages", () => {
    expect(normalizeBulkManagementResult({
      ok: true,
      message: " Updated frontmatter on 1 note. ",
      changed: "1",
      unchanged: "2",
      failed: "1",
      journal_id: " journal-123 ",
      results: [
        { key: " source ", path: " /n/source ", rel_path: " Source.md ", changed: 1 },
      ],
      failures: [
        { key: " missing ", code: " not_found ", message: " note not found " },
      ],
    })).toEqual({
      ok: true,
      message: "Updated frontmatter on 1 note.",
      changed: 1,
      unchanged: 2,
      failed: 1,
      journal_id: "journal-123",
      results: [
        {
          key: "source",
          path: "/n/source",
          rel_path: "Source.md",
          changed: true,
        },
      ],
      failures: [
        {
          key: "missing",
          code: "not_found",
          message: "note not found",
        },
      ],
    });

    expect(normalizeManagementErrorMessage(409, {
      code: "preview_required",
      message: " bulk preview required ",
    })).toBe("bulk preview required");
    expect(normalizeManagementErrorMessage(409, {
      code: "preview_stale",
      message: " bulk preview is stale ",
    })).toBe("bulk preview is stale");
    expect(normalizeManagementErrorMessage(503, { code: "preview_stale" })).toBe(
      "Request failed (503)."
    );
  });
});
