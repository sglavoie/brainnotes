import { hasActiveSharedFilters, readExprParam, readPageQuery, sharedFilterParamKeys } from "./filters";

// Ordinary links let each destination hydrate all search controls from its URL,
// including in another tab. Removing one constraint never resets the others.
export const renderOverviewEmptyHelp = (root: HTMLElement): void => {
  const help = root.querySelector<HTMLElement>("[data-overview-empty-help]");
  if (!help) return;
  const params = new URLSearchParams(window.location.search);
  const facets = hasActiveSharedFilters(params);
  const expression = readExprParam(params) !== "";
  const constraints = [readPageQuery() ? "text" : "", facets ? "filters" : "", expression ? "expression" : ""]
    .filter(Boolean);
  const summary = document.createElement("span");
  summary.textContent = `Searching with: ${constraints.join(", ")}.`;
  help.replaceChildren(summary);
  if (facets) addLink(help, params, sharedFilterParamKeys, "Remove filters");
  if (expression) addLink(help, params, ["expr"], "Remove expression");
  help.hidden = false;
};

const addLink = (help: HTMLElement, params: URLSearchParams, keys: readonly string[], label: string): void => {
  const next = new URLSearchParams(params);
  for (const key of keys) next.delete(key);
  const link = document.createElement("a");
  const query = next.toString();
  link.href = query ? `/library?${query}` : "/library";
  link.textContent = label;
  help.appendChild(link);
};
