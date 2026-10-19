import { jsonResponse, waitForExpect } from "./shared.test-helpers";

export const catalogPayload = () => ({
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

export const detailPayload = (overrides: Record<string, unknown> = {}) => ({
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

export const mountCatalog = (): HTMLElement => {
  document.body.innerHTML = `
    <section data-properties-catalog
      data-properties-endpoint="/api/properties"
      data-properties-detail-endpoint="/api/properties/detail"
      data-properties-csrf="csrf-token">
      <p data-properties-status></p>
      <p data-properties-config hidden></p>
      <dl data-properties-totals></dl>
      <input data-properties-query>
      <input type="checkbox" data-properties-issues-only>
      <button type="button" data-properties-refresh>Refresh</button>
      <ul data-properties-list></ul>
      <section data-properties-proposals hidden>
        <button type="button" data-properties-config-open>brainnotes.toml</button>
        <ul data-properties-proposal-list></ul>
      </section>
      <div data-properties-dialog-host></div>
    </section>
  `;
  const root = document.querySelector("[data-properties-catalog]");
  if (!(root instanceof HTMLElement)) {
    throw new Error("catalog root missing");
  }
  return root;
};

export const mountDetail = (property = "status"): HTMLElement => {
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

export { jsonResponse, waitForExpect };
