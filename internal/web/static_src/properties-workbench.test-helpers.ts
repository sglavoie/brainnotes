import { vi } from "vitest";

import type { PropertyMutationResult, PropertySummary } from "./api";
import { jsonResponse, waitForExpect as sharedWaitForExpect } from "./shared.test-helpers";

export const waitForExpect = (assertion: () => void): Promise<void> =>
  sharedWaitForExpect(assertion, { attempts: 30 });

export const summaryFixture = (overrides: Partial<PropertySummary> = {}): PropertySummary => ({
  name: "status",
  path: "/properties/status",
  configured: true,
  definition: {
    type: "string",
    items: "",
    required: true,
    allowed: ["active", "draft"],
    summary: "type: string, required, allowed: active, draft",
  },
  note_count: 2,
  total_notes: 3,
  coverage: 0.66,
  missing_required: 1,
  observed_kinds: ["string"],
  distinct_values: 2,
  violation_notes: 1,
  violations: 1,
  violation_counts: { missing: 1, type: 0, value: 0 },
  unsupported_values: 0,
  unsupported_notes: 0,
  issues: 1,
  ...overrides,
});

export const mutationFixture = (
  overrides: Partial<PropertyMutationResult> = {}
): PropertyMutationResult => ({
  ok: true,
  generation: 3,
  action: "unset",
  property: "status",
  new_name: "",
  selected: 1,
  affected: 1,
  previews: [
    {
      rel_path: "Notes/Alpha.md",
      changes: [
        {
          kind: "frontmatter",
          key: "status",
          old: { present: true, text: "active" },
          new: { present: false, text: "" },
        },
      ],
    },
  ],
  blockers: [],
  config_diff: [],
  projected_violations: {
    before: { missing: 1, type: 0, value: 0, notes: 1 },
    after: { missing: 2, type: 0, value: 0, notes: 2 },
  },
  preview_id: "receipt-1",
  expires_at: new Date(Date.now() + 300000).toISOString(),
  journal_id: "",
  config: {
    generation: 3,
    source_hash: "abc",
    status: "ok",
    restart_required: [],
    writable: true,
  },
  ...overrides,
});

export const detailPayload = (overrides: Record<string, unknown> = {}) => ({
  ok: true,
  generation: 3,
  property: summaryFixture(),
  values: [
    { value: { kind: "string", text: "active", value: "active", supported: true }, notes: 1 },
    { value: { kind: "string", text: "draft", value: "draft", supported: true }, notes: 1 },
  ],
  rows: [
    {
      rel_path: "Notes/Alpha.md",
      title: "Alpha",
      folder: "Notes",
      path: "/note/notes/alpha",
      hash: "hash-alpha",
      present: true,
      value: { kind: "string", text: "active", value: "active", supported: true },
      issues: [],
      unsupported: false,
    },
    {
      rel_path: "Notes/Beta.md",
      title: "Beta",
      folder: "Notes",
      path: "/note/notes/beta",
      hash: "hash-beta",
      present: true,
      value: { kind: "string", text: "draft", value: "draft", supported: true },
      issues: [],
      unsupported: false,
    },
  ],
  pagination: { limit: 50, offset: 0, total: 2, returned: 2, has_more: false },
  filter: { status: "all" },
  config: {
    generation: 3,
    source_hash: "abc",
    status: "ok",
    restart_required: [],
    writable: true,
  },
  ...overrides,
});

export const mountWorkbench = (token = "tok"): HTMLElement => {
  document.body.innerHTML = `
    <section data-property-detail
      data-property-name="status"
      data-property-csrf="${token}"
      data-property-history-path="#property-history"
      data-properties-detail-endpoint="/api/properties/detail">
      <p data-property-status></p>
      <p data-property-config hidden></p>
      <p data-property-config-invalid hidden></p>
      <p data-property-config-restart hidden></p>
      <p data-property-definition hidden></p>
      <dl data-property-summary></dl>
      <div data-property-proposal hidden></div>
      <section data-property-schema>
        <select data-property-schema-type></select>
        <div data-property-schema-items-row hidden>
          <select data-property-schema-items></select>
        </div>
        <input type="checkbox" data-property-schema-required>
        <textarea data-property-schema-allowed></textarea>
        <p data-property-schema-error hidden></p>
        <button type="button" data-property-schema-save>Save definition</button>
        <button type="button" data-property-schema-delete disabled>Remove definition</button>
        <button type="button" data-property-schema-adopt hidden>Use inferred definition</button>
      </section>
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
      <div>
        <p data-property-selection-count>No notes selected.</p>
        <button type="button" data-property-select-all>Select page</button>
        <button type="button" data-property-clear-selection disabled>Clear selection</button>
        <button type="button" data-property-action="set" disabled>Set value</button>
        <button type="button" data-property-action="replace_value" disabled>Replace value</button>
        <button type="button" data-property-action="unset" disabled>Remove property</button>
        <button type="button" data-property-action="rename_property" disabled>Rename property</button>
        <button type="button" data-property-action="repair" disabled>Repair</button>
      </div>
      <div data-property-editor></div>
      <ul data-property-rows></ul>
      <button type="button" data-property-prev disabled>Previous</button>
      <p data-property-pagination></p>
      <button type="button" data-property-next disabled>Next</button>
      <div data-property-dialog-host></div>
      <div data-property-toast-host></div>
    </section>
  `;
  const root = document.querySelector("[data-property-detail]");
  if (!(root instanceof HTMLElement)) {
    throw new Error("detail root missing");
  }
  return root;
};

type Route = (body: unknown) => unknown;

export const routedFetch = (routes: Record<string, Route>) =>
  vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const key = Object.keys(routes).find((candidate) => url.startsWith(candidate));
    if (!key) {
      return Promise.resolve(jsonResponse({ ok: false, message: "no route" }, 404));
    }
    const raw = typeof init?.body === "string" ? init.body : "";
    let parsed: unknown = raw;
    try {
      parsed = raw === "" ? undefined : JSON.parse(raw);
    } catch {
      parsed = raw;
    }
    const route = routes[key];
    if (!route) {
      return Promise.resolve(jsonResponse({ ok: false, message: "no route" }, 404));
    }
    return Promise.resolve(jsonResponse(route(parsed)));
  });

export const requireElement = <T extends Element>(
  selector: string,
  ctor: new () => T
): T => {
  const found = document.querySelector(selector);
  if (!(found instanceof ctor)) {
    throw new Error(`missing ${selector}`);
  }
  return found;
};

export const submitEditor = (): void => {
  const form = requireElement("[data-property-editor-form]", HTMLFormElement);
  form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
};

export { jsonResponse };
