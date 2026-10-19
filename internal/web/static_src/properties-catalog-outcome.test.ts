// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import { initPropertiesCatalog } from "./properties";
import { catalogPayload, jsonResponse, mountCatalog, waitForExpect } from "./properties.test-helpers";

afterEach(() => {
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

const preview = {
  ok: true,
  action: "schema_upsert",
  property: "tags",
  selected: 0,
  affected: 0,
  previews: [],
  blockers: [],
  config_diff: [{ kind: "add", line: 2, text: "[frontmatter.properties.tags]" }],
  projected_violations: {
    before: { missing: 0, type: 0, value: 0, notes: 0 },
    after: { missing: 0, type: 0, value: 0, notes: 0 },
  },
  preview_id: "preview-1",
  expires_at: "2099-01-01T00:00:00Z",
  config: { generation: 3, status: "ok", writable: true },
};

const confirmProposal = async (root: HTMLElement): Promise<void> => {
  await waitForExpect(() => {
    expect(root.querySelector("[data-properties-proposal-add]")).not.toBeNull();
  });
  root.querySelector<HTMLButtonElement>("[data-properties-proposal-add]")?.click();
  await waitForExpect(() => {
    expect(root.querySelector<HTMLElement>("[data-property-dialog]")?.hidden).toBe(false);
    expect(root.querySelector<HTMLButtonElement>("[data-property-preview-confirm]")?.disabled).toBe(false);
  });
  root.querySelector<HTMLButtonElement>("[data-property-preview-confirm]")?.click();
};

describe("inferred definition outcome", () => {
  it("sends an attempt ID and offers a check when the reply is lost", async () => {
    const root = mountCatalog();
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/api/properties/schema/preview") return Promise.resolve(jsonResponse(preview));
      if (url === "/api/properties/schema/apply") return Promise.reject(new Error("response lost"));
      if (url.startsWith("/api/operations/outcome")) {
        return Promise.resolve(jsonResponse({ ok: true, status: "confirmed", journal_id: "entry-9" }));
      }
      return Promise.resolve(jsonResponse(catalogPayload()));
    });
    initPropertiesCatalog({ fetch: fetchMock });
    await confirmProposal(root);

    await waitForExpect(() => {
      expect(root.querySelector(".write-outcome-check")).not.toBeNull();
    });
    const apply = fetchMock.mock.calls.find(([url]) => String(url) === "/api/properties/schema/apply") as
      unknown as [string, RequestInit] | undefined;
    const body = JSON.parse(String(apply?.[1].body));
    expect(body.operation_id).toMatch(/^[0-9a-f]{32}$/);
    const status = root.querySelector<HTMLElement>("[data-properties-status]");
    expect(status?.textContent).not.toContain("refused");

    const reads = (): number => fetchMock.mock.calls.filter(([url]) => String(url) === "/api/properties").length;
    const before = reads();
    root.querySelector<HTMLButtonElement>(".write-outcome-check")?.click();
    await waitForExpect(() => {
      expect(status?.textContent).toContain("Definition change confirmed in History (entry entry-9).");
      expect(reads()).toBeGreaterThan(before);
    });
    expect(status?.classList.contains("is-success")).toBe(true);
  });

  it("keeps a failed re-read instead of claiming the definition was added", async () => {
    const root = mountCatalog();
    let catalogReads = 0;
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/api/properties/schema/preview") return Promise.resolve(jsonResponse(preview));
      if (url === "/api/properties/schema/apply") {
        return Promise.resolve(jsonResponse({ ...preview, preview_id: "", journal_id: "j-1" }));
      }
      catalogReads += 1;
      return catalogReads === 1
        ? Promise.resolve(jsonResponse(catalogPayload()))
        : Promise.reject(new Error("offline"));
    });
    initPropertiesCatalog({ fetch: fetchMock });
    await confirmProposal(root);

    await waitForExpect(() => {
      expect(catalogReads).toBe(2);
      expect(root.querySelector("[data-properties-status]")?.textContent).toBe("Properties unavailable.");
    });
  });
});
