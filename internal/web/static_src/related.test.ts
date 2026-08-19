// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import { initRelatedNotes } from "./related";
import type { RelatedMode } from "./related-types";

const waitForExpect = async (assertion: () => void): Promise<void> => {
  let lastError: unknown;
  for (let attempt = 0; attempt < 30; attempt += 1) {
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

const jsonResponse = (body: unknown, status = 200): Response =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

const deferred = <T>(): { promise: Promise<T>; resolve: (value: T) => void } => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
};

const identity = (key: string, overrides: Record<string, unknown> = {}): Record<string, unknown> => ({
  key,
  title: `${key} title`,
  path: `Folder/${key}.md`,
  route: `/notes/${key}`,
  ...overrides,
});

const payload = (
  mode: RelatedMode,
  limit: number,
  overrides: Record<string, unknown> = {}
): Record<string, unknown> => ({
  server_date: "2026-08-19",
  source: identity("source"),
  mode,
  has_more: limit < 20,
  results: Array.from({ length: limit }, (_, index) => ({
    candidate: identity(`${mode}-${index + 1}`),
    excerpt: `${mode} excerpt ${index + 1}`,
    relationship: index % 2 === 0 ? "direct" : "two-hop",
    reasons: [{ kind: "text", detail: `${mode} reason ${index + 1}` }],
    useful: false,
  })),
  feedback: [],
  ...overrides,
});

const rootHTML = (): string => `
  <section
    class="panel related-notes"
    data-related-notes
    data-related-source-key="source"
    data-related-endpoint="/api/related"
    data-related-item-endpoint="/api/related/item"
    data-related-csrf="csrf-token"
  >
    <h2>Related Notes</h2>
    <p data-related-status role="status" aria-live="polite">Related notes will load here.</p>
  </section>
`;

const requestOptions = (input: RequestInfo | URL): { mode: RelatedMode; limit: number } => {
  const url = new URL(String(input), "https://example.test");
  return {
    mode: url.searchParams.get("mode") as RelatedMode,
    limit: Number(url.searchParams.get("limit")),
  };
};

const postFields = (init?: RequestInit): URLSearchParams =>
  new URLSearchParams(String(init?.body || ""));

const itemPayload = (
  action: string,
  candidate: string,
  overrides: Record<string, unknown> = {}
): Record<string, unknown> => ({
  ok: true,
  action,
  source: identity("source"),
  candidate: identity(candidate),
  ...(action === "useful" || action === "dismissed" || action === "later" || action === "clear"
    ? { state: action }
    : {}),
  ...overrides,
});

afterEach(() => {
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

describe("related notes controller", () => {
  it("does nothing when the note-page mount is absent", () => {
    const fetchMock = vi.fn();
    initRelatedNotes({ fetch: fetchMock });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("loads Balanced immediately and lazily loads modes with independent limits up to twenty", async () => {
    document.body.innerHTML = rootHTML();
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const { mode, limit } = requestOptions(input);
      return jsonResponse(payload(mode, limit));
    });

    initRelatedNotes({ fetch: fetchMock });
    await waitForExpect(() =>
      expect(document.querySelectorAll('[data-related-results="balanced"] [data-related-card]')).toHaveLength(5)
    );
    expect(requestOptions(fetchMock.mock.calls[0][0])).toEqual({ mode: "balanced", limit: 5 });
    expect(document.querySelector("[data-related-status]")?.classList.contains("is-success")).toBe(true);

    document.querySelector<HTMLButtonElement>('[data-related-show-more="balanced"]')?.click();
    await waitForExpect(() =>
      expect(document.querySelectorAll('[data-related-results="balanced"] [data-related-card]')).toHaveLength(10)
    );
    expect(requestOptions(fetchMock.mock.calls[1][0])).toEqual({ mode: "balanced", limit: 10 });

    document.querySelector<HTMLButtonElement>('[data-related-tab="rediscover"]')?.click();
    await waitForExpect(() =>
      expect(document.querySelectorAll('[data-related-results="rediscover"] [data-related-card]')).toHaveLength(5)
    );
    expect(requestOptions(fetchMock.mock.calls[2][0])).toEqual({ mode: "rediscover", limit: 5 });
    document.querySelector<HTMLButtonElement>('[data-related-show-more="rediscover"]')?.click();
    await waitForExpect(() =>
      expect(document.querySelectorAll('[data-related-results="rediscover"] [data-related-card]')).toHaveLength(10)
    );
    expect(requestOptions(fetchMock.mock.calls[3][0])).toEqual({ mode: "rediscover", limit: 10 });

    document.querySelector<HTMLButtonElement>('[data-related-tab="balanced"]')?.click();
    expect(fetchMock).toHaveBeenCalledTimes(4);
    document.querySelector<HTMLButtonElement>('[data-related-show-more="balanced"]')?.click();
    await waitForExpect(() =>
      expect(document.querySelectorAll('[data-related-results="balanced"] [data-related-card]')).toHaveLength(15)
    );
    document.querySelector<HTMLButtonElement>('[data-related-show-more="balanced"]')?.click();
    await waitForExpect(() =>
      expect(document.querySelectorAll('[data-related-results="balanced"] [data-related-card]')).toHaveLength(20)
    );

    expect(requestOptions(fetchMock.mock.calls[4][0]).limit).toBe(15);
    expect(requestOptions(fetchMock.mock.calls[5][0]).limit).toBe(20);
    expect(document.querySelector<HTMLButtonElement>('[data-related-show-more="balanced"]')?.hidden).toBe(true);
  });

  it("implements tab ARIA ownership and automatic keyboard activation", async () => {
    document.body.innerHTML = rootHTML();
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const { mode, limit } = requestOptions(input);
      return jsonResponse(payload(mode, limit));
    });
    initRelatedNotes({ fetch: fetchMock });

    const balanced = document.querySelector<HTMLButtonElement>('[data-related-tab="balanced"]');
    const rediscover = document.querySelector<HTMLButtonElement>('[data-related-tab="rediscover"]');
    const connect = document.querySelector<HTMLButtonElement>('[data-related-tab="connect"]');
    expect(balanced?.getAttribute("role")).toBe("tab");
    expect(balanced?.getAttribute("aria-controls")).toBe("related-panel-balanced");
    expect(document.querySelector("#related-panel-balanced")?.getAttribute("aria-labelledby")).toBe(
      "related-tab-balanced"
    );
    expect(balanced?.textContent).toContain("A broad mix");

    balanced?.focus();
    balanced?.dispatchEvent(new KeyboardEvent("keydown", { bubbles: true, key: "ArrowRight" }));
    expect(document.activeElement).toBe(rediscover);
    expect(rediscover?.getAttribute("aria-selected")).toBe("true");
    expect(rediscover?.tabIndex).toBe(0);
    expect(balanced?.tabIndex).toBe(-1);
    expect(document.querySelector<HTMLElement>("#related-panel-balanced")?.hidden).toBe(true);
    expect(document.querySelector<HTMLElement>("#related-panel-rediscover")?.hidden).toBe(false);

    rediscover?.dispatchEvent(new KeyboardEvent("keydown", { bubbles: true, key: "End" }));
    expect(document.activeElement).toBe(connect);
    expect(connect?.getAttribute("aria-selected")).toBe("true");
    connect?.dispatchEvent(new KeyboardEvent("keydown", { bubbles: true, key: "Home" }));
    expect(document.activeElement).toBe(balanced);
    expect(balanced?.getAttribute("aria-selected")).toBe("true");

    await waitForExpect(() => expect(fetchMock).toHaveBeenCalledTimes(3));
  });

  it("shows empty and request errors without destroying the last successful panel", async () => {
    document.body.innerHTML = rootHTML();
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const { mode, limit } = requestOptions(input);
      if (mode === "balanced" && limit === 10) {
        return jsonResponse({ code: "conflict", message: "Recall state is unreadable." }, 409);
      }
      if (mode === "rediscover") {
        return jsonResponse(payload(mode, 0, { has_more: false, results: [] }));
      }
      return jsonResponse(payload(mode, limit));
    });
    initRelatedNotes({ fetch: fetchMock });
    await waitForExpect(() =>
      expect(document.querySelectorAll('[data-related-results="balanced"] [data-related-card]')).toHaveLength(5)
    );

    document.querySelector<HTMLButtonElement>('[data-related-show-more="balanced"]')?.click();
    await waitForExpect(() =>
      expect(document.querySelector("[data-related-status]")?.textContent).toContain("Recall state is unreadable")
    );
    expect(document.querySelectorAll('[data-related-results="balanced"] [data-related-card]')).toHaveLength(5);

    document.querySelector<HTMLButtonElement>('[data-related-tab="rediscover"]')?.click();
    await waitForExpect(() => expect(document.querySelector('[data-related-results="rediscover"] .related-empty')).not.toBeNull());
    expect(document.querySelector("[data-related-status]")?.classList.contains("is-empty")).toBe(true);
  });

  it("renders untrusted fields as text and leaves note links unintercepted", async () => {
    document.body.innerHTML = rootHTML();
    const malicious = '<img src=x onerror="alert(1)">';
    const fetchMock = vi.fn(async () =>
      jsonResponse(payload("balanced", 1, {
        has_more: false,
        score: 100,
        results: [
          {
            candidate: identity("candidate", { title: malicious, path: malicious, route: "/notes/safe" }),
            excerpt: `<script>alert("excerpt")</script>`,
            relationship: "shared-neighbor",
            bridge: identity("bridge", { title: malicious, route: "/notes/bridge" }),
            reasons: [
              { kind: "text", detail: malicious },
              { kind: "graph", detail: "A shared link" },
              { kind: "metadata", detail: "A shared tag" },
              { kind: "recall", detail: "A fourth reason" },
            ],
            useful: false,
            score: 0.99,
          },
        ],
      }))
    );
    initRelatedNotes({ fetch: fetchMock });
    await waitForExpect(() => expect(document.querySelector("[data-related-card]")).not.toBeNull());

    const card = document.querySelector<HTMLElement>("[data-related-card]");
    const titleLink = card?.querySelector<HTMLAnchorElement>(".related-card-title a");
    expect(titleLink?.textContent).toBe(malicious);
    expect(titleLink?.getAttribute("href")).toBe("/notes/safe");
    expect(card?.querySelector("img")).toBeNull();
    expect(card?.querySelector("script")).toBeNull();
    expect(card?.querySelector(".related-card-excerpt")?.textContent).toContain("<script>");
    expect(card?.querySelector(".related-card-relationship")?.textContent).toBe("Shared neighbor");
    expect(card?.querySelector(".related-card-bridge a")?.getAttribute("href")).toBe("/notes/bridge");
    expect(card?.querySelectorAll(".related-card-reasons li")).toHaveLength(3);
    expect(card?.textContent).not.toContain("0.99");
    expect(card?.querySelector('[class*="score"], [data-score]')).toBeNull();
    expect(card?.querySelectorAll("[data-related-action]")).toHaveLength(5);
    expect(card?.querySelector('[data-related-action="useful"]')?.getAttribute("aria-pressed")).toBe("false");

    let preventedBeforeDocument = true;
    document.addEventListener(
      "click",
      (event) => {
        preventedBeforeDocument = event.defaultPrevented;
        event.preventDefault();
      },
      { once: true }
    );
    titleLink?.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    expect(preventedBeforeDocument).toBe(false);
  });

  it("toggles Useful, preserves limits, and invalidates already-loaded inactive modes", async () => {
    document.body.innerHTML = rootHTML();
    let useful = false;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === "POST") {
        const form = postFields(init);
        const action = form.get("action") || "";
        expect((init.headers as Record<string, string>)["X-BN-CSRF"]).toBe("csrf-token");
        expect(form.get("source")).toBe("source");
        expect(form.get("candidate")).toBe("balanced-1");
        useful = action === "useful";
        return jsonResponse(itemPayload(action, "balanced-1"));
      }
      const { mode, limit } = requestOptions(input);
      const response = payload(mode, limit);
      if (mode === "balanced") {
        const results = response.results as Array<Record<string, unknown>>;
        results[0].useful = useful;
        response.feedback = useful
          ? [{ candidate: identity("balanced-1"), state: "useful" }]
          : [];
      }
      return jsonResponse(response);
    });
    initRelatedNotes({ fetch: fetchMock });
    await waitForExpect(() => expect(document.querySelector("[data-related-card]")).not.toBeNull());

    document.querySelector<HTMLButtonElement>('[data-related-tab="rediscover"]')?.click();
    await waitForExpect(() =>
      expect(document.querySelectorAll('[data-related-results="rediscover"] [data-related-card]')).toHaveLength(5)
    );
    document.querySelector<HTMLButtonElement>('[data-related-tab="balanced"]')?.click();
    const usefulButton = document.querySelector<HTMLButtonElement>(
      '[data-related-results="balanced"] [data-related-action="useful"]'
    );
    usefulButton?.click();
    await waitForExpect(() =>
      expect(
        document.querySelector('[data-related-results="balanced"] [data-related-action="useful"]')?.getAttribute(
          "aria-pressed"
        )
      ).toBe("true")
    );
    expect(document.querySelector("[data-related-feedback-summary]")?.textContent).toBe("Feedback (1)");
    const latestCall = fetchMock.mock.calls.at(-1);
    expect(latestCall && requestOptions(latestCall[0]).limit).toBe(5);

    document.querySelector<HTMLButtonElement>('[data-related-tab="rediscover"]')?.click();
    await waitForExpect(() => {
      const rediscoverGets = fetchMock.mock.calls.filter(
        ([input, init]) => !init?.method && requestOptions(input).mode === "rediscover"
      );
      expect(rediscoverGets).toHaveLength(2);
    });
    document.querySelector<HTMLButtonElement>('[data-related-tab="balanced"]')?.click();
    await waitForExpect(() =>
      expect(
        document.querySelector('[data-related-results="balanced"] [data-related-action="useful"]')?.getAttribute(
          "aria-pressed"
        )
      ).toBe("true")
    );
    document
      .querySelector<HTMLButtonElement>('[data-related-results="balanced"] [data-related-action="useful"]')
      ?.click();
    await waitForExpect(() =>
      expect(
        document.querySelector('[data-related-results="balanced"] [data-related-action="useful"]')?.getAttribute(
          "aria-pressed"
        )
      ).toBe("false")
    );
    const postActions = fetchMock.mock.calls
      .filter(([, init]) => init?.method === "POST")
      .map(([, init]) => postFields(init).get("action"));
    expect(postActions).toEqual(["useful", "clear"]);
  });

  it("uses server-date Later presets, rejects malformed custom dates, and restores focus after suppression", async () => {
    document.body.innerHTML = rootHTML();
    let suppressed = "";
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === "POST") {
        const form = postFields(init);
        suppressed = form.get("candidate") || "";
        return jsonResponse(
          itemPayload(form.get("action") || "", suppressed, { until: form.get("until") || undefined })
        );
      }
      const { mode } = requestOptions(input);
      const keys = suppressed ? ["balanced-1", "balanced-3", "balanced-4"] : ["balanced-1", "balanced-2", "balanced-3"];
      return jsonResponse(
        payload(mode, 3, {
          server_date: "2026-03-07",
          has_more: true,
          results: keys.map((key) => ({
            candidate: identity(key),
            excerpt: key,
            relationship: "direct",
            reasons: [],
            useful: false,
          })),
          feedback: suppressed ? [{ candidate: identity(suppressed), state: "later", until: "2026-03-08" }] : [],
        })
      );
    });
    initRelatedNotes({ fetch: fetchMock });
    await waitForExpect(() => expect(document.querySelectorAll("[data-related-card]")).toHaveLength(3));
    const second = document.querySelector<HTMLElement>('[data-related-card="balanced-2"]');
    const tomorrow = second?.querySelector<HTMLButtonElement>('[data-related-until="2026-03-08"]');
    const nextWeek = second?.querySelector<HTMLButtonElement>('[data-related-until="2026-03-14"]');
    expect(tomorrow).not.toBeNull();
    expect(nextWeek).not.toBeNull();
    expect(second?.querySelector<HTMLInputElement>("[data-related-later-date]")?.min).toBe("2026-03-08");

    const input = second?.querySelector<HTMLInputElement>("[data-related-later-date]");
    const form = second?.querySelector<HTMLFormElement>("[data-related-later-form]");
    if (input) input.value = "2026-03-07";
    form?.dispatchEvent(new SubmitEvent("submit", { bubbles: true, cancelable: true }));
    expect(document.querySelector("[data-related-status]")?.textContent).toContain("valid date");
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "POST")).toHaveLength(0);

    tomorrow?.click();
    await waitForExpect(() => expect(document.querySelector('[data-related-card="balanced-2"]')).toBeNull());
    expect(postFields(fetchMock.mock.calls.find(([, init]) => init?.method === "POST")?.[1]).get("until")).toBe(
      "2026-03-08"
    );
    expect(document.activeElement?.getAttribute("data-related-card")).toBe("balanced-3");
    expect(document.querySelector("[data-related-status]")?.textContent).toContain("2026-03-08");
  });

  it("renders all feedback and clears off-screen rows without losing the drawer focus target", async () => {
    document.body.innerHTML = rootHTML();
    let cleared = false;
    const feedback = [
      { candidate: identity("balanced-1"), state: "useful" },
      { candidate: identity("offscreen"), state: "dismissed" },
      { candidate: identity("snoozed"), state: "later", until: "2026-08-26" },
    ];
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === "POST") {
        const form = postFields(init);
        expect(form.get("candidate")).toBe("offscreen");
        expect(form.get("action")).toBe("clear");
        cleared = true;
        return jsonResponse(itemPayload("clear", "offscreen"));
      }
      const { mode, limit } = requestOptions(input);
      return jsonResponse(payload(mode, limit, { feedback: cleared ? feedback.filter((entry) => entry.candidate.key !== "offscreen") : feedback }));
    });
    initRelatedNotes({ fetch: fetchMock });
    await waitForExpect(() => expect(document.querySelector("[data-related-feedback-summary]")?.textContent).toBe("Feedback (3)"));
    const drawer = document.querySelector<HTMLDetailsElement>("[data-related-feedback-drawer]");
    if (drawer) drawer.open = true;
    expect(document.querySelector('[data-related-feedback-row="snoozed"] time')?.textContent).toContain("2026-08-26");
    document
      .querySelector<HTMLButtonElement>('[data-related-feedback-row="offscreen"] [data-related-action="clear"]')
      ?.click();
    await waitForExpect(() => expect(document.querySelector("[data-related-feedback-summary]")?.textContent).toBe("Feedback (2)"));
    expect(document.activeElement).toBe(document.querySelector("[data-related-feedback-summary]"));
    expect(drawer?.open).toBe(true);
  });

  it("keeps editor failures local, distinguishes partial warnings, and leaves other cards usable", async () => {
    document.body.innerHTML = rootHTML();
    let attempt = 0;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === "POST") {
        attempt += 1;
        if (attempt === 1) {
          return jsonResponse({ message: "Editor is not configured." }, 412);
        }
        return jsonResponse(
          itemPayload(
            "open_editor",
            postFields(init).get("candidate") || "",
            attempt === 2
              ? { opened: true, recorded: false, warning: "Recall history could not be recorded." }
              : { opened: true, recorded: true }
          )
        );
      }
      const { mode, limit } = requestOptions(input);
      return jsonResponse(payload(mode, limit));
    });
    initRelatedNotes({ fetch: fetchMock });
    await waitForExpect(() => expect(document.querySelector("[data-related-card]")).not.toBeNull());
    const firstOpen = document.querySelector<HTMLButtonElement>(
      '[data-related-card="balanced-1"] [data-related-action="open_editor"]'
    );
    const secondUseful = document.querySelector<HTMLButtonElement>(
      '[data-related-card="balanced-2"] [data-related-action="useful"]'
    );
    firstOpen?.click();
    expect(secondUseful?.disabled).toBe(false);
    await waitForExpect(() => expect(document.querySelector("[data-related-status]")?.textContent).toContain("not configured"));
    expect(firstOpen?.disabled).toBe(false);
    expect(document.querySelector<HTMLButtonElement>('[data-related-tab="rediscover"]')?.disabled).toBe(false);
    firstOpen?.click();
    await waitForExpect(() => expect(document.querySelector("[data-related-status]")?.classList.contains("is-warning")).toBe(true));
    expect(document.querySelector("[data-related-status]")?.textContent).toContain("Recall history");
    firstOpen?.click();
    await waitForExpect(() => expect(document.querySelector("[data-related-status]")?.classList.contains("is-success")).toBe(true));
    expect(document.querySelector("[data-related-status]")?.textContent).toContain("recorded");
  });

  it("keeps the prior card and drawer state after a failed feedback mutation", async () => {
    document.body.innerHTML = rootHTML();
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === "POST") {
        return jsonResponse({ message: "Recall state is unreadable." }, 409);
      }
      const { mode, limit } = requestOptions(input);
      return jsonResponse(
        payload(mode, limit, {
          feedback: [{ candidate: identity("offscreen"), state: "dismissed" }],
        })
      );
    });
    initRelatedNotes({ fetch: fetchMock });
    await waitForExpect(() => expect(document.querySelector("[data-related-card]")).not.toBeNull());
    const dismiss = document.querySelector<HTMLButtonElement>(
      '[data-related-card="balanced-1"] [data-related-action="dismissed"]'
    );
    dismiss?.click();
    await waitForExpect(() => expect(document.querySelector("[data-related-status]")?.textContent).toContain("unreadable"));
    expect(document.querySelector('[data-related-card="balanced-1"]')).not.toBeNull();
    expect(document.querySelector('[data-related-feedback-row="offscreen"]')).not.toBeNull();
    expect(dismiss?.disabled).toBe(false);
  });

  it("refreshes post-mutation state after an overlapping load completes", async () => {
    document.body.innerHTML = rootHTML();
    const expansion = deferred<Response>();
    let dismissed = false;
    let getCount = 0;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === "POST") {
        const form = postFields(init);
        expect(form.get("candidate")).toBe("balanced-1");
        expect(form.get("action")).toBe("dismissed");
        dismissed = true;
        return jsonResponse(itemPayload("dismissed", "balanced-1"));
      }
      getCount += 1;
      const { mode, limit } = requestOptions(input);
      if (getCount === 2) {
        return expansion.promise;
      }
      const response = payload(mode, limit);
      if (dismissed) {
        response.results = (response.results as Array<Record<string, unknown>>).filter(
          (result) => (result.candidate as Record<string, unknown>).key !== "balanced-1"
        );
        response.feedback = [{ candidate: identity("balanced-1"), state: "dismissed" }];
      }
      return jsonResponse(response);
    });

    initRelatedNotes({ fetch: fetchMock });
    await waitForExpect(() => expect(document.querySelectorAll("[data-related-card]")).toHaveLength(5));
    document.querySelector<HTMLButtonElement>('[data-related-show-more="balanced"]')?.click();
    await waitForExpect(() => expect(getCount).toBe(2));
    document
      .querySelector<HTMLButtonElement>('[data-related-card="balanced-1"] [data-related-action="dismissed"]')
      ?.click();
    await waitForExpect(() =>
      expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "POST")).toHaveLength(1)
    );

    expansion.resolve(jsonResponse(payload("balanced", 10)));

    await waitForExpect(() => expect(getCount).toBe(3));
    await waitForExpect(() => expect(document.querySelector('[data-related-card="balanced-1"]')).toBeNull());
    expect(document.querySelectorAll('[data-related-results="balanced"] [data-related-card]')).toHaveLength(9);
    expect(document.querySelector("[data-related-feedback-summary]")?.textContent).toBe("Feedback (1)");
    expect(document.querySelector("[data-related-status]")?.textContent).toBe("Dismissed note.");
    expect(document.querySelector("[data-related-status]")?.classList.contains("is-success")).toBe(true);
    const latestGet = fetchMock.mock.calls.filter(([, init]) => !init?.method).at(-1);
    expect(latestGet && requestOptions(latestGet[0]).limit).toBe(10);
  });

  it("records normal, modified, and new-tab links without preventing navigation", async () => {
    document.body.innerHTML = rootHTML();
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const { mode, limit } = requestOptions(input);
      return jsonResponse(payload(mode, limit));
    });
    const beacon = vi.fn((_url: string, _data?: BodyInit | null) => true);
    initRelatedNotes({ fetch: fetchMock, sendBeacon: beacon });
    await waitForExpect(() => expect(document.querySelector(".related-card-title a")).not.toBeNull());
    const link = document.querySelector<HTMLAnchorElement>(".related-card-title a");
    link?.setAttribute("target", "_blank");
    const events = [
      new MouseEvent("click", { bubbles: true, cancelable: true }),
      new MouseEvent("click", { bubbles: true, cancelable: true, metaKey: true }),
      new MouseEvent("click", { bubbles: true, cancelable: true, ctrlKey: true }),
    ];
    for (const event of events) {
      link?.dispatchEvent(event);
      expect(event.defaultPrevented).toBe(false);
    }
    expect(beacon).toHaveBeenCalledTimes(3);
    expect(beacon.mock.calls.every(([endpoint]) => endpoint === "/api/related/item")).toBe(true);
  });

  it("completes the integrated tabs, expansion, feedback, drawer, tracking, editor, and focus workflow", async () => {
    document.body.innerHTML = rootHTML();
    const feedback = new Map<string, "useful" | "dismissed" | "later">();
    const postActions: string[] = [];
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === "POST") {
        const form = postFields(init);
        const action = form.get("action") || "";
        const candidate = form.get("candidate") || "";
        postActions.push(action);
        if (action === "clear") feedback.delete(candidate);
        if (action === "useful") feedback.set(candidate, "useful");
        return jsonResponse(
          itemPayload(
            action,
            candidate,
            action === "open_editor" ? { opened: true, recorded: true } : {}
          )
        );
      }
      const { mode, limit } = requestOptions(input);
      const response = payload(mode, limit, {
        feedback: Array.from(feedback, ([key, state]) => ({ candidate: identity(key), state })),
      });
      for (const result of response.results as Array<Record<string, unknown>>) {
        result.useful = feedback.get((result.candidate as Record<string, unknown>).key as string) === "useful";
      }
      return jsonResponse(response);
    });
    const beacon = vi.fn((_url: string, _data?: BodyInit | null) => true);

    initRelatedNotes({ fetch: fetchMock, sendBeacon: beacon });
    await waitForExpect(() => expect(document.querySelectorAll('[data-related-results="balanced"] [data-related-card]')).toHaveLength(5));

    const balancedTab = document.querySelector<HTMLButtonElement>('[data-related-tab="balanced"]');
    balancedTab?.focus();
    balancedTab?.dispatchEvent(new KeyboardEvent("keydown", { bubbles: true, key: "ArrowRight" }));
    await waitForExpect(() => expect(document.querySelectorAll('[data-related-results="rediscover"] [data-related-card]')).toHaveLength(5));
    expect(document.activeElement).toBe(document.querySelector('[data-related-tab="rediscover"]'));
    document.querySelector<HTMLButtonElement>('[data-related-tab="connect"]')?.click();
    await waitForExpect(() => expect(document.querySelectorAll('[data-related-results="connect"] [data-related-card]')).toHaveLength(5));

    balancedTab?.click();
    document.querySelector<HTMLButtonElement>('[data-related-show-more="balanced"]')?.click();
    await waitForExpect(() => expect(document.querySelectorAll('[data-related-results="balanced"] [data-related-card]')).toHaveLength(10));

    document.querySelector<HTMLButtonElement>('[data-related-card="balanced-2"] [data-related-action="useful"]')?.click();
    await waitForExpect(() => expect(document.querySelector('[data-related-feedback-summary]')?.textContent).toBe("Feedback (1)"));
    expect(document.querySelector('[data-related-card="balanced-2"] [data-related-action="useful"]')?.getAttribute("aria-pressed")).toBe("true");
    expect(document.activeElement?.getAttribute("data-related-card")).toBe("balanced-2");

    document.querySelector<HTMLButtonElement>('[data-related-tab="rediscover"]')?.click();
    await waitForExpect(() => expect(document.querySelectorAll('[data-related-results="rediscover"] [data-related-card]')).toHaveLength(5));
    const drawer = document.querySelector<HTMLDetailsElement>('[data-related-feedback-drawer]');
    if (drawer) drawer.open = true;
    document.querySelector<HTMLButtonElement>('[data-related-feedback-row="balanced-2"] [data-related-action="clear"]')?.click();
    await waitForExpect(() => expect(document.querySelector('[data-related-feedback-summary]')?.textContent).toBe("Feedback (0)"));
    expect(document.activeElement).toBe(document.querySelector('[data-related-feedback-summary]'));
    expect(drawer?.open).toBe(true);

    document.querySelector<HTMLButtonElement>('[data-related-tab="balanced"]')?.click();
    await waitForExpect(() => expect(document.querySelectorAll('[data-related-results="balanced"] [data-related-card]')).toHaveLength(10));
    const candidateLink = document.querySelector<HTMLAnchorElement>('[data-related-card="balanced-1"] .related-card-title a');
    candidateLink?.setAttribute("target", "_blank");
    candidateLink?.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    expect(beacon).toHaveBeenCalledOnce();

    document.querySelector<HTMLButtonElement>('[data-related-card="balanced-1"] [data-related-action="open_editor"]')?.click();
    await waitForExpect(() => expect(document.querySelector('[data-related-status]')?.textContent).toContain("recorded"));
    expect(postActions).toEqual(["useful", "clear", "open_editor"]);
    expect(fetchMock.mock.calls.some(([input, init]) => !init?.method && requestOptions(input).mode === "balanced" && requestOptions(input).limit === 10)).toBe(true);
  });
});
