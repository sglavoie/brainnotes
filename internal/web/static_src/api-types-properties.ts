// Property intelligence -----------------------------------------------------

// PropertyValue mirrors the server's discriminated value model. `supported` is
// false for YAML that has no scalar spelling (null, a mapping, a nested list);
// those values carry a label in `text` and never a `value`.
export type PropertyValue = {
  kind: string;
  text: string;
  value?: unknown;
  items: PropertyValue[];
  supported: boolean;
};

export type PropertyValueCount = {
  value: PropertyValue;
  notes: number;
};

export type PropertyIssue = {
  kind: string;
  detail: string;
};

export type PropertyViolationCounts = {
  missing: number;
  type: number;
  value: number;
};

export type PropertyDefinition = {
  type: string;
  items?: string;
  required: boolean;
  allowed: string[];
  summary: string;
};

export type PropertyProposal = {
  property: string;
  type: string;
  items?: string;
  notes: number;
  rationale: string;
  toml: string;
};

export type PropertySummary = {
  name: string;
  path: string;
  configured: boolean;
  definition?: PropertyDefinition;
  note_count: number;
  total_notes: number;
  coverage: number;
  missing_required: number;
  observed_kinds: string[];
  distinct_values: number;
  violation_notes: number;
  violations: number;
  violation_counts: PropertyViolationCounts;
  unsupported_values: number;
  unsupported_notes: number;
  issues: number;
  proposal?: PropertyProposal;
};

export type PropertyRow = {
  rel_path: string;
  title: string;
  folder: string;
  path: string;
  // hash is the note's source digest. An immediate edit sends it back as
  // expected_hash so a note that moved on is a conflict rather than a clobber.
  hash: string;
  present: boolean;
  value?: PropertyValue;
  issues: PropertyIssue[];
  unsupported: boolean;
};

export type PropertyPagination = {
  limit: number;
  offset: number;
  total: number;
  returned: number;
  has_more: boolean;
};

export type PropertyRowFilterStatus = "all" | "violations" | "missing" | "unsupported";

export type PropertyRowFilter = {
  status: PropertyRowFilterStatus;
  value?: string;
  q?: string;
};

export type PropertyConfigStatus = {
  generation: number;
  source_hash: string;
  status: string;
  error?: string;
  // restart_required names the startup-only keys that changed on disk. They
  // stay reported until the server is restarted; a reload cannot apply them.
  restart_required: string[];
  writable: boolean;
};

export type PropertyTotals = {
  properties: number;
  configured: number;
  unconfigured: number;
  notes: number;
  violations: number;
  violation_notes: number;
  missing_required: number;
  unsupported_values: number;
  proposals: number;
};

export type PropertiesResult = {
  ok: boolean;
  code?: string;
  message?: string;
  generation: number;
  total_notes: number;
  totals: PropertyTotals;
  properties: PropertySummary[];
  proposals: PropertyProposal[];
  config: PropertyConfigStatus;
};

export type PropertyDetailResult = {
  ok: boolean;
  code?: string;
  message?: string;
  generation: number;
  property: PropertySummary;
  values: PropertyValueCount[];
  rows: PropertyRow[];
  pagination: PropertyPagination;
  filter: PropertyRowFilter;
  config: PropertyConfigStatus;
};

export type PropertyDetailQuery = {
  property: string;
  status?: PropertyRowFilterStatus;
  value?: string;
  q?: string;
  limit?: number;
  offset?: number;
};
