// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import {
  buildBriefInsightForm,
  buildBriefItemForm,
  buildBriefViewForm,
  normalizeBriefInsightResponse,
  normalizeBriefItemResponse,
  normalizeBriefViewResponse,
} from "./api";
import { initDailyBrief } from "./daily-brief";

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

const cardPayload = (
  id: string,
  slot: "continue" | "rediscover" | "connect",
  sourceRelPath: string,
  sourceTitle: string,
  candidate?: { relPath: string; title: string }
): Record<string, unknown> => ({
  ID: id,
  Slot: slot,
  Source: { RelPath: sourceRelPath, Title: sourceTitle },
  Candidate: candidate ? { RelPath: candidate.relPath, Title: candidate.title } : undefined,
  Explanation: `${sourceTitle} explanation`,
  Excerpt: `${sourceTitle} excerpt`,
  Metadata: {
    Relationship: slot === "connect" ? "shared-neighbor" : "",
    Bridge: slot === "connect" ? "Bridge.md" : "",
    Reasons: [`${sourceTitle} reason`],
  },
});

const briefRootHTML = (): string => `
  <section
    data-brief-root
    data-brief-view-endpoint="/api/brief/view"
    data-brief-item-endpoint="/api/brief/item"
    data-brief-insight-endpoint="/api/brief/insight"
    data-brief-csrf="csrf-token"
    data-brief-insight-enabled="true"
  >
    <dd data-brief-activity-visit-days>0</dd>
    <dd data-brief-activity-open-count>0</dd>

    <article class="brief-slot" data-brief-slot="continue">
      <div class="panel-header"><h3>Continue</h3></div>
      <div class="brief-card" data-brief-card data-brief-card-id="continue-1">
        <button type="button" data-brief-action="open" data-brief-action-open data-brief-slot="continue" data-brief-card-id="continue-1">Open</button>
        <button type="button" data-brief-action="another" data-brief-action-another data-brief-slot="continue" data-brief-card-id="continue-1">Another</button>
        <button type="button" data-brief-action="insight" data-brief-action-insight data-brief-slot="continue" data-brief-card-id="continue-1">Ask AI</button>
      </div>
      <p data-brief-slot-status="continue"></p>
    </article>

    <article class="brief-slot" data-brief-slot="rediscover">
      <div class="panel-header"><h3>Rediscover</h3></div>
      <div class="brief-card" data-brief-card data-brief-card-id="rediscover-1">
        <button type="button" data-brief-action="open" data-brief-action-open data-brief-slot="rediscover" data-brief-card-id="rediscover-1">Open</button>
        <button type="button" data-brief-action="another" data-brief-action-another data-brief-slot="rediscover" data-brief-card-id="rediscover-1">Another</button>
        <button type="button" data-brief-action="insight" data-brief-action-insight data-brief-slot="rediscover" data-brief-card-id="rediscover-1">Ask AI</button>
      </div>
      <p data-brief-slot-status="rediscover"></p>
    </article>

    <article class="brief-slot" data-brief-slot="connect">
      <div class="panel-header"><h3>Connect</h3></div>
      <div class="brief-card" data-brief-card data-brief-card-id="connect-1">
        <button type="button" data-brief-action="open" data-brief-action-open data-brief-slot="connect" data-brief-card-id="connect-1">Open</button>
        <button type="button" data-brief-action="another" data-brief-action-another data-brief-slot="connect" data-brief-card-id="connect-1">Another</button>
        <button type="button" data-brief-action="insight" data-brief-action-insight data-brief-slot="connect" data-brief-card-id="connect-1">Ask AI</button>
      </div>
      <p data-brief-slot-status="connect"></p>
    </article>

    <p id="brief-global-status"></p>
  </section>
`;

afterEach(() => {
  vi.unstubAllGlobals();
  document.body.innerHTML = "";
});

describe("brief API normalization", () => {
  it("normalizes brief responses and forms", () => {
    const view = normalizeBriefViewResponse({
      ok: "true",
      activity: {
        Last7Days: { WindowDays: "7", VisitDays: "3", OpenCount: "2" },
      },
    });
    expect(view.ok).toBe(true);
    expect(view.activity.Last7Days).toEqual({
      WindowDays: 7,
      VisitDays: 3,
      OpenCount: 2,
    });

    const item = normalizeBriefItemResponse({
      ok: true,
      action: "another",
      card_id: "continue-1",
      slot: "continue",
      daily: {
        Date: "2026-08-04",
        Slots: [{
          Slot: "continue",
          Card: cardPayload("continue-2", "continue", "Next.md", "Next"),
        }],
      },
      affected: [{
        Slot: "continue",
        Card: cardPayload("continue-2", "continue", "Next.md", "Next"),
      }],
      activity: {
        Last7Days: { WindowDays: 7, VisitDays: 4, OpenCount: 3 },
      },
    });
    expect(item.ok).toBe(true);
    expect(item.card_id).toBe("continue-1");
    expect(item.daily?.Slots[0].Card?.ID).toBe("continue-2");
    expect(item.affected[0].Card?.Source.RelPath).toBe("Next.md");

    const insight = normalizeBriefInsightResponse({
      ok: true,
      enabled: "true",
      card_id: "continue-1",
      slot: "continue",
      output: "raw",
      rendered_html: "<p>safe</p>",
    });
    expect(insight).toMatchObject({
      ok: true,
      enabled: true,
      card_id: "continue-1",
      rendered_html: "<p>safe</p>",
    });

    expect(buildBriefViewForm({ token: "csrf" }).toString()).toBe("token=csrf");
    expect(
      buildBriefItemForm({ cardID: "card-1", action: "open", token: "csrf" }).toString()
    ).toBe("card_id=card-1&action=open&token=csrf");
    expect(buildBriefInsightForm({ cardID: "card-1", token: "csrf" }).toString()).toBe(
      "card_id=card-1&token=csrf"
    );
  });
});

describe("daily brief controller", () => {
  it("records view on init and refreshes activity", async () => {
    document.body.innerHTML = briefRootHTML();
    const fetchMock = vi.fn(async () =>
      jsonResponse({
        ok: true,
        activity: {
          Last7Days: { WindowDays: 7, VisitDays: 2, OpenCount: 1 },
          Last14Days: { WindowDays: 14, VisitDays: 3, OpenCount: 1 },
        },
      })
    );

    initDailyBrief({ fetch: fetchMock as typeof fetch });

    await waitForExpect(() => {
      expect(document.querySelector("[data-brief-activity-visit-days]")?.textContent).toBe("2");
    });
    expect(document.querySelector("[data-brief-activity-open-count]")?.textContent).toBe("1");
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/brief/view");
    expect(String(init.body)).toBe("token=csrf-token");
  });

  it("opens a card, updates activity, and keeps an Opened presentation", async () => {
    document.body.innerHTML = briefRootHTML();
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      if (String(input) === "/api/brief/item") {
        return jsonResponse({
          ok: true,
          action: "open",
          card_id: "continue-1",
          slot: "continue",
          activity: {
            Last7Days: { WindowDays: 7, VisitDays: 3, OpenCount: 2 },
            Last14Days: { WindowDays: 14, VisitDays: 4, OpenCount: 2 },
          },
        });
      }
      return jsonResponse({
        ok: true,
        activity: {
          Last7Days: { WindowDays: 7, VisitDays: 1, OpenCount: 0 },
          Last14Days: { WindowDays: 14, VisitDays: 1, OpenCount: 0 },
        },
      });
    });

    initDailyBrief({ fetch: fetchMock as typeof fetch });
    document.querySelector<HTMLButtonElement>('[data-brief-slot="continue"] [data-brief-action-open]')?.click();

    await waitForExpect(() => {
      expect(document.querySelector('[data-brief-slot="continue"] [data-brief-action-open]')?.textContent).toBe("Opened");
    });
    expect(document.querySelector('[data-brief-slot-status="continue"]')?.textContent).toContain("Opened in editor");
    expect(document.querySelector("[data-brief-activity-open-count]")?.textContent).toBe("2");
  });

  it("replaces continue/connect atomically and restores focus after Another", async () => {
    document.body.innerHTML = briefRootHTML();
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      if (String(input) === "/api/brief/item") {
        const continueCard = cardPayload("continue-2", "continue", "Next.md", "Next");
        const connectCard = cardPayload("connect-2", "connect", "Next.md", "Next", {
          relPath: "Bridge.md",
          title: "Bridge",
        });
        return jsonResponse({
          ok: true,
          action: "another",
          card_id: "continue-1",
          slot: "continue",
          message: "Updated Continue and Connect.",
          activity: {
            Last7Days: { WindowDays: 7, VisitDays: 4, OpenCount: 2 },
            Last14Days: { WindowDays: 14, VisitDays: 5, OpenCount: 2 },
          },
          affected: [
            { Slot: "continue", Card: continueCard },
            { Slot: "connect", Card: connectCard },
          ],
          daily: {
            Date: "2026-08-04",
            Slots: [
              { Slot: "continue", Card: continueCard },
              { Slot: "connect", Card: connectCard },
            ],
          },
        });
      }
      return jsonResponse({
        ok: true,
        activity: {
          Last7Days: { WindowDays: 7, VisitDays: 1, OpenCount: 0 },
          Last14Days: { WindowDays: 14, VisitDays: 1, OpenCount: 0 },
        },
      });
    });

    initDailyBrief({ fetch: fetchMock as typeof fetch });
    document.querySelector<HTMLButtonElement>('[data-brief-slot="continue"] [data-brief-action-another]')?.click();

    await waitForExpect(() => {
      expect(document.querySelector('[data-brief-slot="continue"] [data-brief-card-id="continue-2"]')).not.toBeNull();
    });
    expect(document.querySelector('[data-brief-slot="connect"] [data-brief-card-id="connect-2"]')).not.toBeNull();
    const focused = document.querySelector<HTMLButtonElement>('[data-brief-slot="continue"] [data-brief-action-another]');
    expect(document.activeElement).toBe(focused);
    expect(document.querySelector("#brief-global-status")?.textContent).toContain("Updated Continue and Connect.");
  });

  it("requires disclosure for Ask AI and renders server-safe HTML with raw copy", async () => {
    document.body.innerHTML = briefRootHTML();
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      if (String(input) === "/api/brief/insight") {
        return jsonResponse({
          ok: true,
          enabled: true,
          card_id: "continue-1",
          slot: "continue",
          output: "**Summary**\n<script>alert(1)</script>",
          rendered_html: "<p><strong>Summary</strong></p><p>raw HTML omitted</p>",
        });
      }
      return jsonResponse({
        ok: true,
        activity: {
          Last7Days: { WindowDays: 7, VisitDays: 1, OpenCount: 0 },
          Last14Days: { WindowDays: 14, VisitDays: 1, OpenCount: 0 },
        },
      });
    });
    const confirmMock = vi.fn(() => true);
    const clipboardMock = vi.fn(async () => {});

    initDailyBrief({
      fetch: fetchMock as typeof fetch,
      confirm: confirmMock,
      writeClipboard: clipboardMock,
    });

    document.querySelector<HTMLButtonElement>('[data-brief-slot="continue"] [data-brief-action-insight]')?.click();
    await waitForExpect(() => {
      expect(document.querySelector('[data-brief-slot="continue"] [data-brief-insight-body]')?.innerHTML).toContain("<strong>Summary</strong>");
    });

    const insightHTML = document.querySelector('[data-brief-slot="continue"] [data-brief-insight-body]')?.innerHTML || "";
    expect(confirmMock).toHaveBeenCalledTimes(1);
    expect(insightHTML).toContain("<strong>Summary</strong>");
    expect(insightHTML).not.toContain("<script>");

    document.querySelector<HTMLButtonElement>('[data-brief-slot="continue"] [data-brief-insight-copy]')?.click();
    await waitForExpect(() => {
      expect(clipboardMock).toHaveBeenCalledWith("**Summary**\n<script>alert(1)</script>");
    });
  });
});
