// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import { initPropertyDetail } from "./properties";

import {
  detailPayload,
  mountWorkbench,
  requireElement,
  routedFetch,
  submitEditor,
  waitForExpect,
} from "./properties-workbench.test-helpers";

afterEach(() => {
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

describe("property change outcome check", () => {
  it("re-reads the property once a lost change is confirmed", async () => {
    const routed = routedFetch({
      "/api/properties/detail": () => detailPayload(),
      "/api/operations/outcome": () => ({ ok: true, status: "confirmed", journal_id: "entry-3" }),
    });
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) =>
      String(input) === "/api/properties/value"
        ? Promise.reject(new Error("response lost"))
        : routed(input, init)
    );
    mountWorkbench();
    initPropertyDetail({ fetch: fetchMock });
    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-property-row-edit]")).toHaveLength(2);
    });

    requireElement("[data-property-row-edit='Notes/Alpha.md']", HTMLButtonElement).click();
    requireElement("[data-property-control]", HTMLSelectElement).value = "draft";
    submitEditor();
    await waitForExpect(() => {
      expect(document.querySelector(".write-outcome-check")).not.toBeNull();
    });

    const detailReads = (): number =>
      fetchMock.mock.calls.filter(([url]) => String(url).startsWith("/api/properties/detail")).length;
    const before = detailReads();
    requireElement(".write-outcome-check", HTMLButtonElement).click();
    await waitForExpect(() => {
      expect(requireElement("[data-property-status]", HTMLElement).textContent).toContain(
        "Property change confirmed in History"
      );
      expect(detailReads()).toBeGreaterThan(before);
    });
  });
});
