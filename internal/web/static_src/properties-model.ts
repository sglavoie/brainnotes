import type {
  PropertiesResult,
  PropertyConfigStatus,
  PropertyDetailResult,
  PropertyIssue,
  PropertyPagination,
  PropertyRowFilterStatus,
  PropertySummary,
  PropertyValue,
} from "./api";

export type FetchLike = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;

export type PropertiesDeps = {
  fetch?: FetchLike;
};

export type PropertiesConfig = {
  endpoint: string;
  detailEndpoint: string;
};

export type PropertyDetailConfig = {
  property: string;
  detailEndpoint: string;
  // token is empty on a read-only render. Every mutation control is built only
  // when it is present, so a page that cannot write never offers a write.
  token: string;
  historyPath: string;
};

export type CatalogFilter = {
  query: string;
  issuesOnly: boolean;
};

export type DetailFilter = {
  status: PropertyRowFilterStatus;
  value: string;
  query: string;
  offset: number;
};

export const PROPERTY_ROW_LIMIT = 50;

export const readPropertiesConfig = (root: HTMLElement): PropertiesConfig => ({
  endpoint: root.getAttribute("data-properties-endpoint") || "/api/properties",
  detailEndpoint:
    root.getAttribute("data-properties-detail-endpoint") || "/api/properties/detail",
});

export const readPropertyDetailConfig = (root: HTMLElement): PropertyDetailConfig => ({
  property: root.getAttribute("data-property-name") || "",
  detailEndpoint:
    root.getAttribute("data-properties-detail-endpoint") || "/api/properties/detail",
  token: root.getAttribute("data-property-csrf") || "",
  historyPath: root.getAttribute("data-property-history-path") || "/library",
});

export const emptyDetailFilter = (): DetailFilter => ({
  status: "all",
  value: "",
  query: "",
  offset: 0,
});

export const propertyKindLabel = (kind: string): string => {
  switch (kind) {
    case "wikilink":
      return "Wikilink";
    case "datetime":
      return "Date and time";
    case "unsupported":
      return "Unsupported";
    case "":
      return "Unknown";
    default:
      return kind.charAt(0).toUpperCase() + kind.slice(1);
  }
};

export const definitionLabel = (summary: PropertySummary): string => {
  if (!summary.configured) {
    return "Not configured";
  }
  if (!summary.definition) {
    return "Configured";
  }
  if (summary.definition.summary !== "") {
    return summary.definition.summary;
  }
  return summary.definition.items
    ? `type: ${summary.definition.type} of ${summary.definition.items}`
    : `type: ${summary.definition.type}`;
};

export const coverageLabel = (summary: PropertySummary): string => {
  const percent = Math.round(summary.coverage * 100);
  return `${summary.note_count} of ${summary.total_notes} notes (${percent}%)`;
};

export const issueSummaryLabel = (summary: PropertySummary): string => {
  const parts: string[] = [];
  if (summary.violation_counts.missing > 0) {
    parts.push(`${summary.violation_counts.missing} missing`);
  }
  if (summary.violation_counts.type > 0) {
    parts.push(`${summary.violation_counts.type} wrong type`);
  }
  if (summary.violation_counts.value > 0) {
    parts.push(`${summary.violation_counts.value} disallowed value`);
  }
  if (summary.unsupported_values > 0) {
    parts.push(`${summary.unsupported_values} unsupported`);
  }
  return parts.length === 0 ? "No issues" : parts.join(", ");
};

export const issueLabel = (issue: PropertyIssue): string => {
  const prefix =
    issue.kind === "missing"
      ? "Missing"
      : issue.kind === "type"
        ? "Wrong type"
        : issue.kind === "value"
          ? "Disallowed value"
          : issue.kind;
  return issue.detail === "" ? prefix : `${prefix}: ${issue.detail}`;
};

// valueDisplayText renders a value for display. An unsupported value shows only
// its label, so nothing on screen can be mistaken for a value a write could
// reproduce.
export const valueDisplayText = (value: PropertyValue | undefined): string => {
  if (!value) {
    return "";
  }
  if (!value.supported) {
    return value.text;
  }
  if (value.items.length > 0) {
    return value.items.map((item) => item.text).join(", ");
  }
  return value.text;
};

export const catalogMatches = (summary: PropertySummary, filter: CatalogFilter): boolean => {
  if (filter.issuesOnly && summary.issues === 0) {
    return false;
  }
  const needle = filter.query.trim().toLowerCase();
  if (needle === "") {
    return true;
  }
  return summary.name.toLowerCase().includes(needle);
};

export const filterCatalog = (
  summaries: PropertySummary[],
  filter: CatalogFilter
): PropertySummary[] => summaries.filter((summary) => catalogMatches(summary, filter));

export const catalogStatusMessage = (result: PropertiesResult, shown: number): string => {
  const { totals } = result;
  if (totals.properties === 0) {
    return "No properties found.";
  }
  const scope =
    shown === totals.properties
      ? `${totals.properties} properties`
      : `${shown} of ${totals.properties} properties`;
  const issues = totals.violations + totals.unsupported_values;
  const detail =
    issues === 0
      ? "no issues"
      : `${issues} ${issues === 1 ? "issue" : "issues"} across ${totals.violation_notes} notes`;
  return `${scope} across ${totals.notes} notes, ${detail}.`;
};

export const paginationLabel = (pagination: PropertyPagination): string => {
  if (pagination.total === 0) {
    return "No notes match this filter.";
  }
  const first = pagination.offset + 1;
  const last = pagination.offset + pagination.returned;
  return `Notes ${first}-${last} of ${pagination.total}`;
};

export const detailStatusMessage = (result: PropertyDetailResult): string => {
  const { property } = result;
  const parts = [coverageLabel(property)];
  if (property.distinct_values > 0) {
    parts.push(
      `${property.distinct_values} distinct ${property.distinct_values === 1 ? "value" : "values"}`
    );
  }
  parts.push(issueSummaryLabel(property).toLowerCase());
  return `${parts.join(", ")}.`;
};

export const configStatusMessage = (config: PropertyConfigStatus): string => {
  if (config.status === "ok") {
    return "";
  }
  const detail = config.error ?? "";
  return detail === ""
    ? "brainnotes.toml is invalid; the last valid configuration is still in use."
    : `brainnotes.toml is invalid: ${detail}`;
};

export const nextOffset = (pagination: PropertyPagination): number =>
  pagination.offset + Math.max(pagination.returned, 1);

export const previousOffset = (pagination: PropertyPagination, limit: number): number =>
  Math.max(pagination.offset - Math.max(limit, 1), 0);
