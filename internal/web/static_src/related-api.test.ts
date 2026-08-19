import { describe, expect, it, vi } from "vitest";

import {
  buildRelatedRequestURL,
  normalizeRelatedResponse,
  requestRelated,
} from "./related-api";
import {
  buildRelatedItemForm,
  buildRelatedItemRequest,
  normalizeRelatedItemResponse,
  requestRelatedItem,
  trackRelatedView,
} from "./related-actions";
import type { RelatedItemAction } from "./related-types";
import { addRelatedCalendarDays, validateRelatedLaterDate } from "./related-model";

const identity = (key: string): Record<string, unknown> => ({
  key,
  title: `${key} title`,
  path: `Folder/${key}.md`,
  route: `/notes/${key}`,
});

describe("related API", () => {
  it("normalizes only the score-free fields used by the explorer", () => {
    const response = normalizeRelatedResponse({
      server_date: "2026-08-19",
      source: identity("source"),
      mode: "balanced",
      has_more: true,
      score: 99,
      results: [
        {
          candidate: identity("candidate"),
          excerpt: "A useful excerpt",
          relationship: "shared-neighbor",
          bridge: identity("bridge"),
          useful: true,
          score: 0.987,
          signals: { text: 1 },
          reasons: [
            { kind: "text", detail: "Shared phrase", contribution: 1 },
            { kind: "graph", detail: "Shared neighbor" },
            { kind: "metadata", detail: "Same project" },
            { kind: "recall", detail: "Must be capped" },
          ],
        },
      ],
      feedback: [
        { candidate: identity("candidate"), state: "later", until: "2026-08-20" },
      ],
    });

    expect(response).toEqual({
      serverDate: "2026-08-19",
      source: identity("source"),
      mode: "balanced",
      hasMore: true,
      results: [
        {
          candidate: identity("candidate"),
          excerpt: "A useful excerpt",
          relationship: "shared-neighbor",
          bridge: identity("bridge"),
          useful: true,
          reasons: [
            { kind: "text", detail: "Shared phrase" },
            { kind: "graph", detail: "Shared neighbor" },
            { kind: "metadata", detail: "Same project" },
          ],
        },
      ],
      feedback: [
        { candidate: identity("candidate"), state: "later", until: "2026-08-20" },
      ],
    });
    expect(response?.results[0]).not.toHaveProperty("score");
    expect(response?.results[0]).not.toHaveProperty("signals");
  });

  it("rejects malformed envelopes and drops malformed nested entries", () => {
    expect(normalizeRelatedResponse(null)).toBeNull();
    expect(normalizeRelatedResponse({ mode: "balanced", results: [] })).toBeNull();
    expect(normalizeRelatedResponse({ source: identity("source"), mode: "surprise" })).toBeNull();

    const response = normalizeRelatedResponse({
      source: identity("source"),
      mode: "connect",
      results: [
        null,
        { candidate: { key: "missing-route", path: "Bad.md" } },
        {
          candidate: identity("good"),
          relationship: "invented",
          reasons: [null, { kind: "invented", detail: "bad" }, { kind: "text", detail: "good" }],
        },
      ],
      feedback: [
        { candidate: identity("good"), state: "invented" },
        { candidate: identity("good"), state: "useful" },
      ],
    });

    expect(response?.results).toHaveLength(1);
    expect(response?.results[0].relationship).toBe("");
    expect(response?.results[0].reasons).toEqual([{ kind: "text", detail: "good" }]);
    expect(response?.feedback).toEqual([{ candidate: identity("good"), state: "useful" }]);
  });

  it("builds encoded bounded requests and rejects mismatched responses", async () => {
    expect(buildRelatedRequestURL("/api/related", "Folder/source note", "rediscover", 99)).toBe(
      "/api/related?source=Folder%2Fsource+note&mode=rediscover&limit=20"
    );
    expect(buildRelatedRequestURL("/api/related?fresh=1", "source", "balanced", 0)).toBe(
      "/api/related?fresh=1&source=source&mode=balanced&limit=1"
    );

    const fetcher = vi.fn(async () =>
      new Response(JSON.stringify({ source: identity("other"), mode: "balanced", results: [] }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      })
    );
    await expect(requestRelated(fetcher, "/api/related", "source", "balanced", 5)).rejects.toThrow(
      "invalid response"
    );
  });
});

describe("related item API", () => {
  const actions: RelatedItemAction[] = [
    "view",
    "open_editor",
    "useful",
    "dismissed",
    "later",
    "clear",
  ];

  it("builds form and CSRF requests for every action", async () => {
    for (const action of actions) {
      const request = {
        source: "folder/source",
        candidate: "candidate note",
        action,
        token: "csrf-token",
        ...(action === "later" ? { until: "2026-08-20" } : {}),
      };
      const form = buildRelatedItemForm(request);
      expect(form.get("source")).toBe("folder/source");
      expect(form.get("candidate")).toBe("candidate note");
      expect(form.get("action")).toBe(action);
      expect(form.get("token")).toBe("csrf-token");
      expect(form.get("until")).toBe(action === "later" ? "2026-08-20" : null);

      const init = buildRelatedItemRequest(request);
      expect(init.method).toBe("POST");
      expect((init.headers as Record<string, string>)["X-BN-CSRF"]).toBe("csrf-token");
      expect(String(init.body)).toContain(`action=${action}`);
    }
  });

  it("uses strict UTC calendar arithmetic for DST-adjacent and malformed dates", () => {
    expect(addRelatedCalendarDays("2026-03-07", 1)).toBe("2026-03-08");
    expect(addRelatedCalendarDays("2026-03-07", 7)).toBe("2026-03-14");
    expect(addRelatedCalendarDays("2026-10-31", 1)).toBe("2026-11-01");
    expect(addRelatedCalendarDays("2028-02-28", 1)).toBe("2028-02-29");
    expect(validateRelatedLaterDate("2026-03-07", "2026-03-07")).toContain("valid date");
    expect(validateRelatedLaterDate("2026-03-07", "2026-02-29")).toContain("valid date");
    expect(validateRelatedLaterDate("2026-03-07", "03/08/2026")).toContain("valid date");
    expect(validateRelatedLaterDate("2026-03-07", "2026-03-08")).toBe("");
  });

  it("normalizes successes and surfaces server and malformed-response failures", async () => {
    expect(
      normalizeRelatedItemResponse({
        ok: true,
        action: "open_editor",
        source: identity("source"),
        candidate: identity("candidate"),
        opened: true,
        recorded: false,
        warning: "History unavailable.",
      })
    ).toEqual({
      ok: true,
      action: "open_editor",
      source: identity("source"),
      candidate: identity("candidate"),
      state: undefined,
      until: undefined,
      opened: true,
      recorded: false,
      warning: "History unavailable.",
    });

    const failure = vi.fn(async () =>
      new Response(JSON.stringify({ message: "Editor is not configured." }), {
        status: 412,
        headers: { "Content-Type": "application/json" },
      })
    );
    await expect(
      requestRelatedItem(failure, "/api/related/item", {
        source: "source",
        candidate: "candidate",
        action: "open_editor",
        token: "csrf-token",
      })
    ).rejects.toThrow("Editor is not configured");

    const malformed = vi.fn(async () =>
      new Response(JSON.stringify({ ok: true, action: "clear" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      })
    );
    await expect(
      requestRelatedItem(malformed, "/api/related/item", {
        source: "source",
        candidate: "candidate",
        action: "clear",
        token: "csrf-token",
      })
    ).rejects.toThrow("invalid response");
  });

  it("tracks views with a beacon and falls back to keepalive without awaiting navigation", async () => {
    const fetcher = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) =>
      new Response(null, { status: 204 })
    );
    const beacon = vi.fn((_url: string, _data?: BodyInit | null) => true);
    trackRelatedView(fetcher, beacon, "/api/related/item", {
      source: "source",
      candidate: "candidate",
      token: "csrf-token",
    });
    expect(beacon).toHaveBeenCalledOnce();
    expect(fetcher).not.toHaveBeenCalled();
    expect(beacon.mock.calls[0][1]).toBeInstanceOf(Blob);

    trackRelatedView(fetcher, () => false, "/api/related/item", {
      source: "source",
      candidate: "candidate",
      token: "csrf-token",
    });
    expect(fetcher).toHaveBeenCalledOnce();
    const init = fetcher.mock.calls[0][1];
    expect(init?.keepalive).toBe(true);
    expect(String(init?.body)).toContain("action=view");
  });
});
