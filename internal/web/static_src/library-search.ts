import { browserQueryChangeEventName, readExprParam, readPageQuery } from "./filters";

// Keep the existing search, facet and expression controllers authoritative.
// These links remove only their own constraint, retaining sort, cap and filters.
const renderConstraint = (root: HTMLElement, selector: string, key: string, label: string, value: string): void => {
  const host = root.querySelector<HTMLElement>(selector);
  if (!host) return;
  host.replaceChildren();
  host.hidden = value === "";
  if (!value) return;
  const link = document.createElement("a");
  const params = new URLSearchParams(window.location.search);
  params.delete(key);
  link.href = params.size ? `/library?${params}` : "/library";
  link.className = "library-constraint";
  link.textContent = `${label}: ${value} ×`;
  link.setAttribute("aria-label", `Remove ${label.toLowerCase()}: ${value}`);
  host.appendChild(link);
};

export const initLibrarySearch = (): void => {
  const root = document.querySelector<HTMLElement>("[data-library-search]");
  if (!root) return;
  const expression = root.querySelector<HTMLDetailsElement>("[data-library-expression]");
  const indicator = root.querySelector<HTMLElement>("[data-library-expression-indicator]");
  const refresh = (): void => {
    const expr = readExprParam();
    renderConstraint(root, "[data-library-text-constraint]", "q", "Text", readPageQuery());
    renderConstraint(root, "[data-library-expr-constraint]", "expr", "Expression", expr);
    if (indicator) indicator.textContent = expr ? "· active" : "· optional";
    // An expression arriving through a saved view or browser history must be
    // visible. Do not close the disclosure while someone is drafting a query.
    if (expression && expr) expression.open = true;
  };
  refresh();
  window.addEventListener(browserQueryChangeEventName, refresh);
  window.addEventListener("popstate", refresh);
};
