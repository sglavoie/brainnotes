// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import { initRelatedNotes } from "./related";
import {
  identity,
  itemPayload,
  jsonResponse,
  payload,
  postFields,
  requestOptions,
  rootHTML,
  waitForExpect,
} from "./related.test-helpers";

afterEach(() => {
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

describe("related notes feedback", () => {
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
});
