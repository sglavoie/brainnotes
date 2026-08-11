export type ApiFacetOption = {
  value: string;
  count: number;
};

export type ApiFacetIssueOption = ApiFacetOption & {
  param: string;
  label: string;
};

export type FacetsResponse = {
  tags: ApiFacetOption[];
  categories: ApiFacetOption[];
  status: ApiFacetOption[];
  types: ApiFacetOption[];
  folders: ApiFacetOption[];
  properties: ApiFacetOption[];
  issues: ApiFacetIssueOption[];
};

export type FacetGroup = {
  key?: keyof FacetsResponse;
  label: string;
  param?: string;
  repeat?: boolean;
  issue?: boolean;
};

export type FacetFilterOption = {
  param: string;
  value: string;
  label: string;
  count: number;
  groupLabel: string;
  repeat: boolean;
};
