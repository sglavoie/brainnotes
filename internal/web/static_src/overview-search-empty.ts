import { hasActiveSharedFilters, readExprParam, readPageQuery, sharedFilterParamKeys } from "./filters";

// Ordinary links let each destination hydrate all search controls from its URL,
// including in another tab. Removing one constraint never resets the others.
export const renderOverviewEmptyHelp = (root: HTMLElement): void => {
  const help = root.querySelector<HTMLElement>("[data-overview-empty-help]");
  if (!help) return;
  const params = new URLSearchParams(window.location.search);
  const facets = hasActiveSharedFilters(params);
  const expression = readExprParam(params) !== "";
  const constraints = [readPageQuery() ? "search text" : "", facets ? "filters" : "", expression ? "expression" : ""]
    .filter(Boolean);
  const status = root.querySelector<HTMLElement>("[data-overview-search-status]");
  if (status) {
    status.textContent = `${status.textContent?.replace(/\.$/, "")}; try changing your ${new Intl.ListFormat("en", { type: "conjunction" }).format(constraints)}.`;
  }
  help.replaceChildren();
  help.className = "empty-state-actions";
  const reset = document.createElement("a");
  reset.href = "/library";
  reset.className = "bn-button bn-primary";
  reset.textContent = "Reset search";
  reset.dataset.emptyReset = "";
  help.appendChild(reset);
  if (readPageQuery()) addLink(help, params, ["q"], "Remove search text");
  if (facets) addLink(help, params, sharedFilterParamKeys, "Remove filters");
  if (expression) addLink(help, params, ["expr"], "Remove expression");
  addCreateLink(help);
  help.hidden = false;
};

const addCreateLink = (help: HTMLElement): void => {
  const title = readPageQuery();
  if (!title || document.documentElement.dataset.readOnly === "true") return;
  const link = document.createElement("a");
  link.href = `/new?${new URLSearchParams({ title, return_to: window.location.pathname + window.location.search + window.location.hash })}`;
  link.className = "bn-button bn-quiet";
  link.textContent = `Create a note titled “${title}”…`;
  link.dataset.emptyCreateNote = "";
  help.appendChild(link);
};

const addLink = (help: HTMLElement, params: URLSearchParams, keys: readonly string[], label: string): void => {
  const next = new URLSearchParams(params);
  for (const key of keys) next.delete(key);
  const link = document.createElement("a");
  const query = next.toString();
  link.href = query ? `/library?${query}` : "/library";
  link.className = "bn-button bn-quiet";
  link.textContent = label;
  help.appendChild(link);
};
