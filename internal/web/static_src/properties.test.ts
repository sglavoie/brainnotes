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
  initPropertiesCatalog,
  initPropertyDetail,
  issueLabel,
  paginationLabel,
  propertyKindLabel,
  valueDisplayText,
} from "./properties";

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

const catalogPayload = () => ({
  ok: true,
  generation: 3,
  total_notes: 4,
  totals: {
    properties: 3,
    configured: 2,
    unconfigured: 1,
    notes: 4,
    violations: 2,
    violation_notes: 2,
    missing_required: 1,
    unsupported_values: 1,
    proposals: 1,
  },
  properties: [
    {
      name: "status",
      path: "/properties/status",
      configured: true,
      definition: {
        type: "string",
        required: true,
        allowed: ["active", "draft"],
        summary: "type: string, required, allowed: active, draft",
      },
      note_count: 3,
      total_notes: 4,
      coverage: 0.75,
      missing_required: 1,
      observed_kinds: ["string"],
      distinct_values: 2,
      violation_notes: 2,
      violations: 2,
      violation_counts: { missing: 1, type: 0, value: 1 },
      unsupported_values: 0,
      unsupported_notes: 0,
      issues: 2,
    },
    {
      name: "meta",
      path: "/properties/meta",
      configured: false,
      note_count: 1,
      total_notes: 4,
      coverage: 0.25,
      missing_required: 0,
      observed_kinds: ["unsupported"],
      distinct_values: 1,
      violation_notes: 0,
      violations: 0,
      violation_counts: { missing: 0, type: 0, value: 0 },
      unsupported_values: 1,
      unsupported_notes: 1,
      issues: 1,
    },
    {
      name: "tags",
      path: "/properties/tags",
      configured: false,
      note_count: 2,
      total_notes: 4,
      coverage: 0.5,
      missing_required: 0,
      observed_kinds: ["list", "string"],
      distinct_values: 2,
      violation_notes: 0,
      violations: 0,
      violation_counts: { missing: 0, type: 0, value: 0 },
      unsupported_values: 0,
      unsupported_notes: 0,
      issues: 0,
      proposal: {
        property: "tags",
        type: "list",
        items: "string",
        notes: 2,
        rationale: "All 2 notes store a list of string values.",
        toml: "[frontmatter.properties.tags]\ntype = \"list\"\nitems = \"string\"",
      },
    },
  ],
  proposals: [
    {
      property: "tags",
      type: "list",
      items: "string",
      notes: 2,
      rationale: "All 2 notes store a list of string values.",
      toml: "[frontmatter.properties.tags]\ntype = \"list\"\nitems = \"string\"",
    },
  ],
  config: { generation: 3, status: "ok", writable: true },
});

const detailPayload = (overrides: Record<string, unknown> = {}) => ({
  ok: true,
  generation: 3,
  property: catalogPayload().properties[0],
  values: [
    { value: { kind: "string", text: "active", value: "active", supported: true }, notes: 2 },
    { value: { kind: "string", text: "retired", value: "retired", supported: true }, notes: 1 },
  ],
  rows: [
    {
      rel_path: "Notes/Alpha.md",
      title: "Alpha",
      folder: "Notes",
      path: "/note/notes/alpha",
      present: true,
      value: { kind: "string", text: "active", value: "active", supported: true },
      issues: [],
      unsupported: false,
    },
    {
      rel_path: "Notes/Gamma.md",
      title: "Gamma",
      folder: "Notes",
      path: "/note/notes/gamma",
      present: false,
      issues: [{ kind: "missing", detail: "required property is absent" }],
      unsupported: false,
    },
  ],
  pagination: { limit: 50, offset: 0, total: 2, returned: 2, has_more: false },
  filter: { status: "all" },
  config: { generation: 3, status: "ok", writable: true },
  ...overrides,
});

const mountCatalog = (): HTMLElement => {
  document.body.innerHTML = `
    <section data-properties-catalog
      data-properties-endpoint="/api/properties"
      data-properties-detail-endpoint="/api/properties/detail">
      <p data-properties-status></p>
      <p data-properties-config hidden></p>
      <dl data-properties-totals></dl>
      <input data-properties-query>
      <input type="checkbox" data-properties-issues-only>
      <button type="button" data-properties-refresh>Refresh</button>
      <ul data-properties-list></ul>
      <section data-properties-proposals hidden>
        <ul data-properties-proposal-list></ul>
      </section>
    </section>
  `;
  const root = document.querySelector("[data-properties-catalog]");
  if (!(root instanceof HTMLElement)) {
    throw new Error("catalog root missing");
  }
  return root;
};

const mountDetail = (property = "status"): HTMLElement => {
  document.body.innerHTML = `
    <section data-property-detail
      data-property-name="${property}"
      data-properties-detail-endpoint="/api/properties/detail">
      <p data-property-status></p>
      <p data-property-config hidden></p>
      <p data-property-definition hidden></p>
      <dl data-property-summary></dl>
      <div data-property-proposal hidden></div>
      <ul data-property-values></ul>
      <select data-property-filter-status>
        <option value="all">All</option>
        <option value="violations">Violations</option>
        <option value="missing">Missing</option>
        <option value="unsupported">Unsupported</option>
      </select>
      <input data-property-filter-query>
      <button type="button" data-property-filter-clear hidden>Clear</button>
      <p data-property-active-value hidden></p>
      <ul data-property-rows></ul>
      <button type="button" data-property-prev disabled>Previous</button>
      <p data-property-pagination></p>
      <button type="button" data-property-next disabled>Next</button>
    </section>
  `;
  const root = document.querySelector("[data-property-detail]");
  if (!(root instanceof HTMLElement)) {
    throw new Error("detail root missing");
  }
  return root;
};

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

describe("catalog controller", () => {
  it("renders the catalog issue-first with proposals", async () => {
    const root = mountCatalog();
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(catalogPayload()));

    initPropertiesCatalog({ fetch: fetchMock });

    await waitForExpect(() => {
      const names = Array.from(root.querySelectorAll("[data-properties-name]")).map((el) =>
        el.getAttribute("data-properties-name")
      );
      expect(names).toEqual(["status", "meta", "tags"]);
    });
    expect(fetchMock).toHaveBeenCalledWith("/api/properties", expect.anything());

    const link = root.querySelector(".properties-item-name");
    expect(link?.getAttribute("href")).toBe("/properties/status");

    const proposals = root.querySelector("[data-properties-proposals]");
    expect(proposals instanceof HTMLElement && proposals.hidden).toBe(false);
    expect(root.querySelector(".properties-proposal-toml")?.textContent).toContain(
      "[frontmatter.properties.tags]"
    );
    expect(root.querySelector("[data-properties-totals]")?.textContent).toContain("Violations");
  });

  it("renders no inactive mutation controls", async () => {
    const root = mountCatalog();
    initPropertiesCatalog({ fetch: vi.fn().mockResolvedValue(jsonResponse(catalogPayload())) });

    await waitForExpect(() => {
      expect(root.querySelectorAll("[data-properties-item]").length).toBe(3);
    });
    const list = root.querySelector("[data-properties-list]");
    expect(list?.querySelectorAll("button").length).toBe(0);
    expect(root.querySelectorAll("button[disabled]").length).toBe(0);
  });

  it("filters rendered rows without refetching", async () => {
    const root = mountCatalog();
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(catalogPayload()));
    initPropertiesCatalog({ fetch: fetchMock });

    await waitForExpect(() => {
      expect(root.querySelectorAll("[data-properties-item]").length).toBe(3);
    });

    const query = root.querySelector("[data-properties-query]");
    if (!(query instanceof HTMLInputElement)) {
      throw new Error("query input missing");
    }
    query.value = "tag";
    query.dispatchEvent(new Event("input"));

    expect(
      Array.from(root.querySelectorAll("[data-properties-name]")).map((el) =>
        el.getAttribute("data-properties-name")
      )
    ).toEqual(["tags"]);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("reports an error status when the endpoint fails", async () => {
    const root = mountCatalog();
    initPropertiesCatalog({
      fetch: vi.fn().mockResolvedValue(
        jsonResponse({ ok: false, code: "internal", message: "boom" }, 500)
      ),
    });

    await waitForExpect(() => {
      const status = root.querySelector("[data-properties-status]");
      expect(status?.classList.contains("is-error")).toBe(true);
    });
  });

  it("surfaces an invalid configuration", async () => {
    const root = mountCatalog();
    const payload = catalogPayload();
    payload.config = {
      generation: 3,
      status: "invalid",
      error: "unknown type",
      writable: false,
    } as typeof payload.config;
    initPropertiesCatalog({ fetch: vi.fn().mockResolvedValue(jsonResponse(payload)) });

    await waitForExpect(() => {
      const config = root.querySelector("[data-properties-config]");
      expect(config instanceof HTMLElement && config.hidden).toBe(false);
      expect(config?.textContent).toContain("unknown type");
    });
  });
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
