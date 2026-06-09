// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import {
  buildReviewItemForm,
  managementMutationEventName,
  normalizeReviewAPIResponse,
  type ReviewItem,
} from "./api";
import {
  buildReviewItemFetchRequest,
  buildReviewOpenFetchRequest,
  initReviewPanel,
  reviewRouteKeyFromPath,
} from "./review-panel";

const waitForExpect = async (assertion: () => void): Promise<void> => {
  let lastError: unknown;
  for (let attempt = 0; attempt < 20; attempt += 1) {
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

const reviewItem = (overrides: Partial<ReviewItem> = {}): ReviewItem => ({
  id: "review_1",
  signature: "sig-1",
  kind: "broken_target",
  priority: 30,
  title: "Broken target: missing",
  summary: "1 unresolved link to missing",
  detail: "missing",
  count: 1,
  affected: [{
    title: "Source",
    rel_path: "Work/Source.md",
    path: "/n/work/source",
    folder: "Work",
    folder_path: "/?folder=Work",
    location: "line 5",
  }],
  ...overrides,
});

const reviewPayload = (items: ReviewItem[], message = ""): unknown => ({
  ok: true,
  message: message || undefined,
  items,
  counts: {
    active: items.length,
    done_hidden: 2,
    snoozed_hidden: 1,
    total_generated: items.length + 3,
  },
  warnings: [{
    code: "review_state_unavailable",
    message: "Review state could not be loaded.",
  }],
});

const renderPanel = (csrfToken = "csrf-token"): void => {
  document.body.innerHTML = `
    <section
      data-review-panel
      data-review-endpoint="/api/review"
      data-review-item-endpoint="/api/review/item"
      data-review-csrf="${csrfToken}"
      aria-busy="false"
    >
      <dd data-review-count-active>0</dd>
      <dd data-review-count-done-hidden>0</dd>
      <dd data-review-count-snoozed-hidden>0</dd>
      <dd data-review-count-total-generated>0</dd>
      <p data-review-status></p>
      <p data-review-loading hidden></p>
      <p data-review-error hidden></p>
      <p data-review-empty hidden></p>
      <ul data-review-warnings hidden></ul>
      <ul data-review-list hidden></ul>
    </section>
  `;
};

afterEach(() => {
  document.body.innerHTML = "";
  vi.unstubAllGlobals();
});

describe("review API helpers", () => {
  it("normalizes API payloads and builds review request forms", () => {
    const result = normalizeReviewAPIResponse({
      ok: "true",
      message: "Updated",
      items: [{
        id: " review_1 ",
        signature: " sig ",
        kind: " broken_target ",
        priority: "30",
        title: " Broken ",
        summary: " Summary ",
        detail: " Detail ",
        count: "2",
        affected: [{
          title: " Source ",
          rel_path: " Work/Source.md ",
          path: " /n/work/source ",
          folder: " Work ",
          folder_path: " /?folder=Work ",
          location: " line 5 ",
        }, {}],
      }, { id: "" }],
      counts: {
        active: "1",
        done_hidden: "2",
        snoozed_hidden: "3",
        total_generated: "4",
      },
      warnings: [{ code: " state ", message: " warning " }, {}],
    });

    expect(result.ok).toBe(true);
    expect(result.items).toHaveLength(1);
    expect(result.items[0]).toMatchObject({
      id: "review_1",
      kind: "broken_target",
      priority: 30,
      count: 2,
    });
    expect(result.items[0].affected).toEqual([{
      title: "Source",
      rel_path: "Work/Source.md",
      path: "/n/work/source",
      folder: "Work",
      folder_path: "/?folder=Work",
      location: "line 5",
    }]);
    expect(result.counts).toEqual({
      active: 1,
      done_hidden: 2,
      snoozed_hidden: 3,
      total_generated: 4,
    });
    expect(result.warnings).toEqual([{ code: "state", message: "warning" }]);

    const form = buildReviewItemForm({
      id: "review_1",
      action: "snooze",
      token: "csrf-token",
      snooze: "week",
    });
    expect(form.toString()).toBe(
      "id=review_1&action=snooze&token=csrf-token&snooze=week"
    );

    const request = buildReviewItemFetchRequest(
      "/api/review/item",
      "csrf-token",
      "review_1",
      "done"
    );
    expect(request.url).toBe("/api/review/item");
    expect(request.init.method).toBe("POST");
    expect(request.init.body).toBe("id=review_1&action=done&token=csrf-token");

    const openRequest = buildReviewOpenFetchRequest("work/source", "csrf-token");
    expect(openRequest.url).toBe("/api/open");
    expect(openRequest.init.body).toBe("key=work%2Fsource&token=csrf-token");
    expect(reviewRouteKeyFromPath("/n/Work/Source%20Note")).toBe("Work/Source Note");
    expect(reviewRouteKeyFromPath("/media/source")).toBe("");
    expect(reviewRouteKeyFromPath("/n/source%2Fbad")).toBe("");
  });
});

describe("review panel DOM behavior", () => {
  it("loads and renders items, counts, warnings, details, and editor actions", async () => {
    renderPanel();
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      if (String(input) === "/api/open") {
        return new Response(null, { status: 204 });
      }
      return jsonResponse(reviewPayload([
        reviewItem({
          affected: [
            ...reviewItem().affected,
            {
              title: "Generated report",
              rel_path: "reports/report.html",
              path: "/media/report.html",
            },
          ],
        }),
      ]));
    });

    initReviewPanel({ fetch: fetchMock as typeof fetch });
    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-review-item]")).toHaveLength(1);
    });

    expect(document.querySelector("[data-review-count-active]")?.textContent).toBe("1");
    expect(document.querySelector("[data-review-count-done-hidden]")?.textContent).toBe("2");
    expect(document.querySelector("[data-review-count-snoozed-hidden]")?.textContent).toBe("1");
    expect(document.querySelector("[data-review-count-total-generated]")?.textContent).toBe("4");
    expect(document.querySelector("[data-review-status]")?.textContent).toBe("1 active review item.");
    expect(document.querySelector("[data-review-warning-code]")?.getAttribute(
      "data-review-warning-code"
    )).toBe("review_state_unavailable");
    expect(document.querySelector("summary")?.textContent).toBe("View details");
    expect(document.querySelector(".review-item-title")?.textContent).toBe("Broken target: missing");
    expect(document.querySelectorAll("[data-review-open-key]")).toHaveLength(1);
    expect(document.querySelector("[data-review-open-key]")?.getAttribute(
      "data-review-open-key"
    )).toBe("work/source");
    expect(Array.from(document.querySelectorAll("[data-review-action]")).map(
      (button) => button.textContent
    )).toEqual(["Done", "Snooze tomorrow", "Snooze next week"]);

    document.querySelector<HTMLButtonElement>("[data-review-open-key]")?.click();
    await waitForExpect(() => {
      expect(document.querySelector("[data-review-status]")?.textContent).toBe(
        "Opened in editor. Refresh after saving changes."
      );
    });

    const [, openInit] = fetchMock.mock.calls[1] as unknown as [string, RequestInit];
    expect(fetchMock.mock.calls[1][0]).toBe("/api/open");
    expect(openInit).toMatchObject({ method: "POST" });
    expect(openInit.body).toBe("key=work%2Fsource&token=csrf-token");
  });

  it("posts review item actions and renders the updated queue", async () => {
    renderPanel();
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input) === "/api/review/item") {
        expect(String(init?.body)).toBe(
          "id=review_1&action=snooze&token=csrf-token&snooze=week"
        );
        return jsonResponse({
          ok: true,
          message: "Snoozed review item for a week.",
          items: [],
          counts: {
            active: 0,
            done_hidden: 2,
            snoozed_hidden: 2,
            total_generated: 4,
          },
        });
      }
      return jsonResponse(reviewPayload([reviewItem()]));
    });

    initReviewPanel({ fetch: fetchMock as typeof fetch });
    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-review-item]")).toHaveLength(1);
    });

    document.querySelector<HTMLButtonElement>(
      "[data-review-action='snooze'][data-review-snooze='week']"
    )?.click();
    await waitForExpect(() => {
      expect(document.querySelector("[data-review-status]")?.textContent).toBe(
        "Snoozed review item for a week."
      );
    });

    expect(document.querySelectorAll("[data-review-item]")).toHaveLength(0);
    expect(document.querySelector<HTMLElement>("[data-review-empty]")?.hidden).toBe(false);
    expect(document.querySelector("[data-review-count-active]")?.textContent).toBe("0");
    expect(document.querySelector("[data-review-count-snoozed-hidden]")?.textContent).toBe("2");
  });

  it("surfaces request errors", async () => {
    renderPanel();
    const fetchMock = vi.fn(async () =>
      jsonResponse({
        ok: false,
        code: "state",
        message: "Review state failed.",
      }, 500)
    );

    initReviewPanel({ fetch: fetchMock as typeof fetch });
    await waitForExpect(() => {
      expect(document.querySelector("[data-review-error]")?.textContent).toBe(
        "Review state failed."
      );
    });

    expect(document.querySelector<HTMLElement>("[data-review-error]")?.hidden).toBe(false);
    expect(document.querySelector("[data-review-status]")?.classList.contains("is-error")).toBe(true);
  });

  it("refreshes after management mutations", async () => {
    renderPanel();
    const fetchMock = vi.fn(async () => {
      if (fetchMock.mock.calls.length === 1) {
        return jsonResponse(reviewPayload([reviewItem({ title: "First item" })]));
      }
      return jsonResponse(reviewPayload([reviewItem({ title: "Refreshed item" })]));
    });

    initReviewPanel({ fetch: fetchMock as typeof fetch });
    await waitForExpect(() => {
      expect(document.querySelector(".review-item-title")?.textContent).toBe("First item");
    });

    window.dispatchEvent(new CustomEvent(managementMutationEventName));
    await waitForExpect(() => {
      expect(document.querySelector(".review-item-title")?.textContent).toBe("Refreshed item");
    });
    expect(document.querySelector("[data-review-status]")?.textContent).toBe("Review refreshed.");
  });

  it("omits editor actions when no affected note has a safe route key", async () => {
    renderPanel();
    const fetchMock = vi.fn(async () =>
      jsonResponse(reviewPayload([
        reviewItem({
          affected: [{
            title: "Unsafe",
            rel_path: "Unsafe.md",
            path: "/media/unsafe.md",
          }],
        }),
      ]))
    );

    initReviewPanel({ fetch: fetchMock as typeof fetch });
    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-review-item]")).toHaveLength(1);
    });

    expect(document.querySelectorAll("[data-review-open-key]")).toHaveLength(0);
  });
});
