// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import { initPropertyDetail } from "./properties";
import { detailPayload, jsonResponse, mountDetail, waitForExpect } from "./properties.test-helpers";

afterEach(() => {
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

const deferred = () => {
  let resolve: (value: Response) => void = () => {};
  const promise = new Promise<Response>((done) => { resolve = done; });
  return { promise, resolve };
};

describe("detail request sequencing", () => {
  it("drops an earlier answer that arrives after the latest one", async () => {
    const root = mountDetail();
    const first = deferred();
    const fetchMock = vi.fn()
      .mockImplementationOnce(() => first.promise)
      .mockImplementation(() => Promise.resolve(jsonResponse(detailPayload({
        rows: [],
        pagination: { limit: 50, offset: 0, total: 0, returned: 0, has_more: false },
      }))));
    initPropertyDetail({ fetch: fetchMock });

    const select = root.querySelector("[data-property-filter-status]");
    if (!(select instanceof HTMLSelectElement)) throw new Error("status filter missing");
    select.value = "violations";
    select.dispatchEvent(new Event("change"));

    await waitForExpect(() => {
      expect(root.getAttribute("aria-busy")).toBe("false");
      expect(root.querySelectorAll("[data-property-row]").length).toBe(0);
    });
    const status = root.querySelector("[data-property-status]")?.textContent;

    first.resolve(jsonResponse(detailPayload()));
    await new Promise((done) => setTimeout(done, 20));
    expect(root.querySelectorAll("[data-property-row]").length).toBe(0);
    expect(root.querySelector("[data-property-status]")?.textContent).toBe(status);
    expect(root.getAttribute("aria-busy")).toBe("false");
  });

  it("keeps the page busy until the latest read answers", async () => {
    const root = mountDetail();
    const second = deferred();
    const fetchMock = vi.fn()
      .mockImplementationOnce(() => Promise.resolve(jsonResponse(detailPayload())))
      .mockImplementationOnce(() => second.promise);
    initPropertyDetail({ fetch: fetchMock });
    await waitForExpect(() => {
      expect(root.querySelectorAll("[data-property-row]").length).toBe(2);
    });

    const select = root.querySelector("[data-property-filter-status]");
    if (!(select instanceof HTMLSelectElement)) throw new Error("status filter missing");
    select.value = "missing";
    select.dispatchEvent(new Event("change"));
    await waitForExpect(() => {
      expect(root.getAttribute("aria-busy")).toBe("true");
    });

    second.resolve(jsonResponse(detailPayload()));
    await waitForExpect(() => {
      expect(root.getAttribute("aria-busy")).toBe("false");
    });
  });
});
