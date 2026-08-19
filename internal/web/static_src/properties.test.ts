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

describe("property API contracts", () => {
  it("normalizes the catalog payload", () => {
    const result = normalizePropertiesResponse(catalogPayload());

    expect(result.ok).toBe(true);
    expect(result.generation).toBe(3);
    expect(result.total_notes).toBe(4);
    expect(result.totals.proposals).toBe(1);
    expect(result.properties.map((summary) => summary.name)).toEqual([
      "status",
      "meta",
      "tags",
    ]);
    expect(result.properties[0].definition?.allowed).toEqual(["active", "draft"]);
    expect(result.properties[1].definition).toBeUndefined();
    expect(result.proposals[0].items).toBe("string");
  });

  it("turns a conservative proposal into a schema definition", () => {
    expect(inferredDefinition(catalogPayload().proposals[0])).toEqual({
      type: "list",
      items: "string",
      required: false,
      allowed: [],
    });
    expect(
      inferredDefinition({
        property: "bad",
        type: "list",
        notes: 1,
        rationale: "",
        toml: "",
      })
    ).toBeNull();
  });

  it("normalizes an error envelope", () => {
    const result = normalizePropertiesResponse({
      ok: false,
      code: "not_found",
      message: "properties: nope",
    });

    expect(result.ok).toBe(false);
    expect(result.code).toBe("not_found");
    expect(result.properties).toEqual([]);
  });

  it("survives a malformed payload", () => {
    const result = normalizePropertiesResponse("not an object");

    expect(result.ok).toBe(false);
    expect(result.properties).toEqual([]);
    expect(result.totals.properties).toBe(0);
  });

  it("keeps an unsupported value label-only", () => {
    const value = normalizePropertyValue({
      kind: "unsupported",
      text: "<map>",
      value: { nested: 1 },
      supported: false,
    });

    expect(value.supported).toBe(false);
    expect(value.text).toBe("<map>");
    expect(value.value).toBeUndefined();
  });

  it("normalizes list values into their elements", () => {
    const value = normalizePropertyValue({
      kind: "list",
      text: "go, web",
      supported: true,
      items: [
        { kind: "string", text: "go", value: "go", supported: true },
        { kind: "string", text: "web", value: "web", supported: true },
      ],
    });

    expect(value.items.map((item) => item.text)).toEqual(["go", "web"]);
    expect(valueDisplayText(value)).toBe("go, web");
  });

  it("normalizes the detail payload", () => {
    const result = normalizePropertyDetailResponse(detailPayload());

    expect(result.property.name).toBe("status");
    expect(result.values).toHaveLength(2);
    expect(result.rows).toHaveLength(2);
    expect(result.rows[1].present).toBe(false);
    expect(result.rows[1].value).toBeUndefined();
    expect(result.pagination.limit).toBe(50);
    expect(result.filter.status).toBe("all");
  });

  it("falls back to the all filter for an unknown status", () => {
    expect(normalizePropertyRowFilterStatus("weird")).toBe("all");
    expect(normalizePropertyRowFilterStatus("violations")).toBe("violations");
  });

  it("builds detail URLs that omit defaults", () => {
    expect(buildPropertyDetailURL("/api/properties/detail", { property: "status" })).toBe(
      "/api/properties/detail?property=status"
    );
    expect(
      buildPropertyDetailURL("/api/properties/detail", {
        property: "my prop",
        status: "violations",
        value: "active",
        q: " alpha ",
        limit: 50,
        offset: 100,
      })
    ).toBe(
      "/api/properties/detail?property=my+prop&filter=violations&value=active&q=alpha&limit=50&offset=100"
    );
  });
});
