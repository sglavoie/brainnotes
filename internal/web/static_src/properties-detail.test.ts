// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import {
  buildPropertyDetailURL,
  normalizePropertiesResponse,
  normalizePropertyDetailResponse,
  normalizePropertyRowFilterStatus,
  normalizePropertyValue,
} from "./api";
import {
  catalogStatusMessage,
  configStatusMessage,
  coverageLabel,
  detailStatusMessage,
  filterCatalog,
  buildOpenConfigFetchRequest,
  inferredDefinition,
  initPropertiesCatalog,
  initPropertyDetail,
  issueLabel,
  paginationLabel,
  propertyKindLabel,
  valueDisplayText,
} from "./properties";

import {
  catalogPayload,
  detailPayload,
  jsonResponse,
  mountCatalog,
  mountDetail,
  waitForExpect,
} from "./properties.test-helpers";

afterEach(() => {
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

describe("detail controller", () => {
  it("renders values and rows, marking a missing value", async () => {
    const root = mountDetail();
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(detailPayload()));

    initPropertyDetail({ fetch: fetchMock });

    await waitForExpect(() => {
      expect(root.querySelectorAll("[data-property-row]").length).toBe(2);
    });
    expect(fetchMock.mock.calls[0][0]).toBe(
      "/api/properties/detail?property=status&limit=50"
    );
    expect(root.querySelectorAll("[data-property-value]").length).toBe(2);
    const rows = root.querySelectorAll("[data-property-row]");
    expect(rows[1].querySelector(".properties-row-value")?.textContent).toBe("No value");
    expect(rows[1].querySelector("[data-property-issue]")?.textContent).toBe(
      "Missing: required property is absent"
    );
    expect(root.querySelector("[data-property-pagination]")?.textContent).toBe(
      "Notes 1-2 of 2"
    );
  });

  it("requests the selected status filter", async () => {
    const root = mountDetail();
    const fetchMock = vi
      .fn()
      .mockImplementation(() => Promise.resolve(jsonResponse(detailPayload())));
    initPropertyDetail({ fetch: fetchMock });

    await waitForExpect(() => {
      expect(fetchMock).toHaveBeenCalledTimes(1);
    });

    const select = root.querySelector("[data-property-filter-status]");
    if (!(select instanceof HTMLSelectElement)) {
      throw new Error("status filter missing");
    }
    select.value = "violations";
    select.dispatchEvent(new Event("change"));

    await waitForExpect(() => {
      expect(fetchMock.mock.calls[1][0]).toBe(
        "/api/properties/detail?property=status&filter=violations&limit=50"
      );
    });
  });

  it("toggles a value filter from the value list", async () => {
    const root = mountDetail();
    const fetchMock = vi
      .fn()
      .mockImplementation(() => Promise.resolve(jsonResponse(detailPayload())));
    initPropertyDetail({ fetch: fetchMock });

    await waitForExpect(() => {
      expect(root.querySelectorAll("[data-property-value]").length).toBe(2);
    });

    const button = root.querySelector("[data-property-value]");
    if (!(button instanceof HTMLButtonElement)) {
      throw new Error("value button missing");
    }
    button.click();

    await waitForExpect(() => {
      expect(fetchMock.mock.calls[1][0]).toBe(
        "/api/properties/detail?property=status&value=active&limit=50"
      );
    });
    await waitForExpect(() => {
      const active = root.querySelector("[data-property-active-value]");
      expect(active instanceof HTMLElement && active.hidden).toBe(false);
    });

    const again = root.querySelector("[data-property-value]");
    if (!(again instanceof HTMLButtonElement)) {
      throw new Error("value button missing after rerender");
    }
    again.click();

    await waitForExpect(() => {
      expect(fetchMock.mock.calls[2][0]).toBe(
        "/api/properties/detail?property=status&limit=50"
      );
    });
  });

  it("disables the value filter for an unsupported value", async () => {
    const root = mountDetail("meta");
    initPropertyDetail({
      fetch: vi.fn().mockResolvedValue(
        jsonResponse(
          detailPayload({
            values: [
              { value: { kind: "unsupported", text: "<map>", supported: false }, notes: 1 },
            ],
            rows: [
              {
                rel_path: "Notes/Gamma.md",
                title: "Gamma",
                folder: "Notes",
                path: "/note/notes/gamma",
                present: true,
                value: { kind: "unsupported", text: "<map>", supported: false },
                issues: [],
                unsupported: true,
              },
            ],
          })
        )
      ),
    });

    await waitForExpect(() => {
      expect(root.querySelectorAll("[data-property-value]").length).toBe(1);
    });
    const button = root.querySelector("[data-property-value]");
    expect(button instanceof HTMLButtonElement && button.disabled).toBe(true);
    const row = root.querySelector("[data-property-row]");
    expect(row?.hasAttribute("data-property-row-unsupported")).toBe(true);
    expect(row?.querySelector(".properties-row-value")?.textContent).toBe("<map>");
  });

  it("pages forward and back", async () => {
    const root = mountDetail();
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        jsonResponse(
          detailPayload({
            pagination: { limit: 50, offset: 0, total: 260, returned: 50, has_more: true },
          })
        )
      )
      .mockResolvedValueOnce(
        jsonResponse(
          detailPayload({
            pagination: { limit: 50, offset: 50, total: 260, returned: 50, has_more: true },
          })
        )
      )
      .mockResolvedValue(
        jsonResponse(
          detailPayload({
            pagination: { limit: 50, offset: 0, total: 260, returned: 50, has_more: true },
          })
        )
      );

    initPropertyDetail({ fetch: fetchMock });

    const next = root.querySelector("[data-property-next]");
    const prev = root.querySelector("[data-property-prev]");
    if (!(next instanceof HTMLButtonElement) || !(prev instanceof HTMLButtonElement)) {
      throw new Error("pagination buttons missing");
    }

    await waitForExpect(() => {
      expect(next.disabled).toBe(false);
      expect(prev.disabled).toBe(true);
    });

    next.click();
    await waitForExpect(() => {
      expect(fetchMock.mock.calls[1][0]).toBe(
        "/api/properties/detail?property=status&limit=50&offset=50"
      );
      expect(prev.disabled).toBe(false);
    });

    prev.click();
    await waitForExpect(() => {
      expect(fetchMock.mock.calls[2][0]).toBe(
        "/api/properties/detail?property=status&limit=50"
      );
    });
  });

  it("reports an error status when the detail endpoint fails", async () => {
    const root = mountDetail();
    initPropertyDetail({
      fetch: vi.fn().mockResolvedValue(
        jsonResponse({ ok: false, code: "not_found", message: "properties: nope" }, 404)
      ),
    });

    await waitForExpect(() => {
      const status = root.querySelector("[data-property-status]");
      expect(status?.classList.contains("is-error")).toBe(true);
    });
  });

  it("does nothing without a mounted panel", () => {
    document.body.innerHTML = "";
    const fetchMock = vi.fn();

    initPropertiesCatalog({ fetch: fetchMock });
    initPropertyDetail({ fetch: fetchMock });

    expect(fetchMock).not.toHaveBeenCalled();
  });
});

