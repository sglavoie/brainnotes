// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import { initRelatedNotes } from "./related";
import {
  identity,
  jsonResponse,
  payload,
  requestOptions,
  rootHTML,
  waitForExpect,
} from "./related.test-helpers";

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
});
