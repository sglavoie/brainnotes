// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import { buildOpenConfigFetchRequest, initPropertiesCatalog } from "./properties";

import {
  catalogPayload,
  jsonResponse,
  mountCatalog,
  waitForExpect,
} from "./properties.test-helpers";

afterEach(() => {
  document.body.innerHTML = "";
  vi.restoreAllMocks();
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
    const stats = root.querySelectorAll("[data-properties-totals] > .properties-stat");
    expect(stats).toHaveLength(7);
    for (const stat of stats) {
      expect(stat.querySelector(":scope > dt")).not.toBeNull();
      expect(stat.querySelector(":scope > dd")).not.toBeNull();
    }
  });

  it("renders an active add control for each inferred definition", async () => {
    const root = mountCatalog();
    initPropertiesCatalog({ fetch: vi.fn().mockResolvedValue(jsonResponse(catalogPayload())) });

    await waitForExpect(() => {
      expect(root.querySelectorAll("[data-properties-item]").length).toBe(3);
    });
    const list = root.querySelector("[data-properties-list]");
    expect(list?.querySelectorAll("button").length).toBe(0);
    expect(root.querySelectorAll("[data-properties-proposal-add]")).toHaveLength(1);
    expect(root.querySelectorAll("button[disabled]").length).toBe(0);
  });
});

describe("catalog controller definitions", () => {
  it("previews and applies an inferred definition", async () => {
    const root = mountCatalog();
    const preview = {
      ok: true,
      action: "schema_upsert",
      property: "tags",
      selected: 0,
      affected: 0,
      previews: [],
      blockers: [],
      config_diff: [
        { kind: "add", line: 2, text: "[frontmatter.properties.tags]" },
      ],
      projected_violations: {
        before: { missing: 0, type: 0, value: 0, notes: 0 },
        after: { missing: 0, type: 0, value: 0, notes: 0 },
      },
      preview_id: "preview-1",
      expires_at: "2099-01-01T00:00:00Z",
      config: { generation: 3, status: "ok", writable: true },
    };
    const applied = {
      ...preview,
      preview_id: "",
      journal_id: "journal-1",
      generation: 4,
    };
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse(catalogPayload()))
      .mockResolvedValueOnce(jsonResponse(preview))
      .mockResolvedValueOnce(jsonResponse(applied))
      .mockResolvedValueOnce(jsonResponse(catalogPayload()));

    initPropertiesCatalog({ fetch: fetchMock });
    await waitForExpect(() => {
      expect(root.querySelector("[data-properties-proposal-add]")).not.toBeNull();
    });
    root.querySelector<HTMLButtonElement>("[data-properties-proposal-add]")?.click();

    await waitForExpect(() => {
      expect(fetchMock).toHaveBeenCalledTimes(2);
      expect(root.querySelector<HTMLDialogElement>("[data-property-dialog]")?.open).toBe(true);
      expect(root.querySelector<HTMLButtonElement>("[data-property-preview-confirm]")?.disabled)
        .toBe(false);
    });
    const previewInit = fetchMock.mock.calls[1]?.[1] as RequestInit;
    expect(fetchMock.mock.calls[1]?.[0]).toBe("/api/properties/schema/preview");
    expect(JSON.parse(String(previewInit.body))).toEqual({
      op: "upsert",
      property: "tags",
      definition: { type: "list", items: "string", required: false, allowed: [] },
    });

    root.querySelector<HTMLButtonElement>("[data-property-preview-confirm]")?.click();
    await waitForExpect(() => {
      expect(fetchMock).toHaveBeenCalledTimes(4);
    });
    expect(fetchMock.mock.calls[2]?.[0]).toBe("/api/properties/schema/apply");
    expect(root.querySelector("[data-properties-status]")?.textContent).toBe(
      "Added tags to brainnotes.toml."
    );
  });
});

describe("catalog controller tools", () => {
  it("opens brainnotes.toml through the configured editor endpoint", async () => {
    const root = mountCatalog();
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse(catalogPayload()))
      .mockResolvedValueOnce(new Response(null, { status: 204 }));

    initPropertiesCatalog({ fetch: fetchMock });
    await waitForExpect(() => {
      expect(root.querySelectorAll("[data-properties-item]")).toHaveLength(3);
    });
    root.querySelector<HTMLButtonElement>("[data-properties-config-open]")?.click();

    await waitForExpect(() => {
      expect(fetchMock).toHaveBeenCalledTimes(2);
    });
    const init = fetchMock.mock.calls[1]?.[1] as RequestInit;
    expect(fetchMock.mock.calls[1]?.[0]).toBe("/api/config/open");
    expect(init).toEqual(buildOpenConfigFetchRequest("csrf-token"));
    expect(root.querySelector("[data-properties-status]")?.textContent).toBe(
      "Opened brainnotes.toml in the configured editor."
    );
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


