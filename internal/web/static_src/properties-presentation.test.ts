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

describe("property presentation", () => {
  it("labels kinds for display", () => {
    expect(propertyKindLabel("wikilink")).toBe("Wikilink");
    expect(propertyKindLabel("datetime")).toBe("Date and time");
    expect(propertyKindLabel("list")).toBe("List");
    expect(propertyKindLabel("")).toBe("Unknown");
  });

  it("summarizes coverage and issues", () => {
    const result = normalizePropertiesResponse(catalogPayload());

    expect(coverageLabel(result.properties[0])).toBe("3 of 4 notes (75%)");
    expect(catalogStatusMessage(result, 3)).toBe(
      "3 properties across 4 notes, 3 issues across 2 notes."
    );
    expect(catalogStatusMessage(result, 1)).toBe(
      "1 of 3 properties across 4 notes, 3 issues across 2 notes."
    );
  });

  it("describes pagination", () => {
    expect(
      paginationLabel({ limit: 50, offset: 0, total: 0, returned: 0, has_more: false })
    ).toBe("No notes match this filter.");
    expect(
      paginationLabel({ limit: 50, offset: 50, total: 260, returned: 50, has_more: true })
    ).toBe("Notes 51-100 of 260");
  });

  it("describes the detail summary", () => {
    const result = normalizePropertyDetailResponse(detailPayload());

    expect(detailStatusMessage(result)).toBe(
      "3 of 4 notes (75%), 2 distinct values, 1 missing, 1 disallowed value."
    );
  });

  it("labels issues", () => {
    expect(issueLabel({ kind: "missing", detail: "required" })).toBe("Missing: required");
    expect(issueLabel({ kind: "type", detail: "" })).toBe("Wrong type");
  });

  it("reports an invalid configuration", () => {
    expect(
      configStatusMessage({
        generation: 3,
        source_hash: "abc",
        status: "ok",
        restart_required: [],
        writable: true,
      })
    ).toBe("");
    expect(
      configStatusMessage({
        generation: 3,
        source_hash: "abc",
        status: "invalid",
        error: "bad type",
        restart_required: [],
        writable: false,
      })
    ).toBe("brainnotes.toml is invalid: bad type");
  });

  it("filters the catalog by name and issue state", () => {
    const result = normalizePropertiesResponse(catalogPayload());

    expect(
      filterCatalog(result.properties, { query: "tag", issuesOnly: false }).map((s) => s.name)
    ).toEqual(["tags"]);
    expect(
      filterCatalog(result.properties, { query: "ta", issuesOnly: false }).map((s) => s.name)
    ).toEqual(["status", "meta", "tags"]);
    expect(
      filterCatalog(result.properties, { query: "", issuesOnly: true }).map((s) => s.name)
    ).toEqual(["status", "meta"]);
    expect(
      filterCatalog(result.properties, { query: "STATUS", issuesOnly: true }).map((s) => s.name)
    ).toEqual(["status"]);
  });
});


