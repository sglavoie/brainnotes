import { normalizeParamValue, repeatedFilterParams } from "./filters";
import { asRecord, readCount } from "./api-internal";
import type { FacetFilterOption, FacetGroup } from "./api-types";

export const facetGroups: FacetGroup[] = [
  { key: "tags", label: "Tags", param: "tag", repeat: true },
  { key: "categories", label: "Categories", param: "category", repeat: true },
  { key: "status", label: "Status", param: "status" },
  { key: "types", label: "Types", param: "type" },
  { key: "folders", label: "Folders", param: "folder" },
  { key: "properties", label: "Properties", param: "has" },
  { key: "issues", label: "Issues", issue: true },
];

export const labelForIssueParam = (param: string): string => {
  switch (param) {
    case "broken":
      return "Broken links";
    case "dead_end":
      return "Dead ends";
    case "orphan":
      return "Orphans";
    default:
      return param;
  }
};

export const fallbackGroupForParam = (param: string): FacetGroup => {
  for (const group of facetGroups) {
    if (group.issue) {
      continue;
    }
    if (group.param === param) {
      return group;
    }
  }
  if (param === "broken" || param === "dead_end" || param === "orphan") {
    return { label: "Issues", issue: true };
  }
  return { label: "Filters" };
};

export const fallbackFacetOption = (param: string, value: string): FacetFilterOption => {
  const group = fallbackGroupForParam(param);
  const label = group.issue ? labelForIssueParam(param) : value;
  return {
    param,
    value,
    label,
    count: 0,
    groupLabel: group.label,
    repeat: repeatedFilterParams.has(param),
  };
};

export const normalizeFacetOptions = (payload: unknown): FacetFilterOption[] => {
  const data = asRecord(payload) || {};
  const options: FacetFilterOption[] = [];
  for (const group of facetGroups) {
    const rawValues = group.key ? data[group.key] : undefined;
    const values: unknown[] = Array.isArray(rawValues) ? rawValues : [];
    for (const item of values) {
      const record = asRecord(item);
      if (!record) {
        continue;
      }
      const param = group.issue ? normalizeParamValue(record.param) : normalizeParamValue(group.param);
      const value = normalizeParamValue(record.value);
      if (param === "" || value === "") {
        continue;
      }
      options.push({
        param,
        value,
        label: group.issue ? normalizeParamValue(record.label) || labelForIssueParam(param) : value,
        count: readCount(record.count),
        groupLabel: group.label,
        repeat: Boolean(group.repeat),
      });
    }
  }
  return options.sort((left, right) => {
    if (left.groupLabel !== right.groupLabel) {
      return left.groupLabel.localeCompare(right.groupLabel);
    }
    if (right.count !== left.count) {
      return right.count - left.count;
    }
    return left.label.localeCompare(right.label);
  });
};
