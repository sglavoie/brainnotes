import { fallbackFacetOption, type FacetFilterOption } from "./api";
import { normalizeParamValue, sharedFilterParamKeys } from "./filters";

export type FacetSelection = Map<string, FacetFilterOption>;

export const facetOptionKey = (option: FacetFilterOption): string =>
  `${option.param}\u0000${option.value}`;

export const facetOptionMatchesQuery = (
  option: FacetFilterOption,
  query: string
): boolean => {
  const normalizedQuery = query.trim().toLowerCase();
  if (normalizedQuery === "") {
    return true;
  }
  const haystack = `${option.groupLabel} ${option.label} ${option.value}`.toLowerCase();
  return haystack.includes(normalizedQuery);
};

export const withFacetOptionSelected = (
  selection: FacetSelection,
  option: FacetFilterOption
): FacetSelection => {
  const next = new Map(selection);
  if (!option.repeat) {
    for (const selectedOption of Array.from(next.values())) {
      if (selectedOption.param === option.param) {
        next.delete(facetOptionKey(selectedOption));
      }
    }
  }
  next.set(facetOptionKey(option), option);
  return next;
};

export const facetSelectionFromParams = (params: URLSearchParams): FacetSelection => {
  let selection: FacetSelection = new Map();
  for (const param of sharedFilterParamKeys) {
    for (const rawValue of params.getAll(param)) {
      const value = normalizeParamValue(rawValue);
      if (value === "") {
        continue;
      }
      selection = withFacetOptionSelected(selection, fallbackFacetOption(param, value));
    }
  }
  return selection;
};

export const hydrateFacetSelection = (
  selection: FacetSelection,
  catalogOptions: FacetFilterOption[]
): FacetSelection => {
  const catalog = new Map(catalogOptions.map((option) => [facetOptionKey(option), option]));
  const next: FacetSelection = new Map();
  for (const option of selection.values()) {
    next.set(facetOptionKey(option), catalog.get(facetOptionKey(option)) || option);
  }
  return next;
};

export const paramsForFacetSelection = (selection: FacetSelection): URLSearchParams => {
  const params = new URLSearchParams();
  for (const option of selection.values()) {
    params.append(option.param, option.value);
  }
  return params;
};

