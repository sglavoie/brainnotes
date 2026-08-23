// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import { initRelatedNotes } from "./related";
import {
  deferred,
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

describe("related notes workflow", () => {
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
