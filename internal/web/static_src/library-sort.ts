import { dispatchFiltersChange, updateBrowserQuery } from "./filters";

export const initLibrarySort = (): void => {
  const select = document.querySelector<HTMLSelectElement>("[data-library-sort]");
  if (!select) return;
  const initial = new URLSearchParams(window.location.search);
  const current = initial.get("sort") || "";
  const anchor = initial.get("relative_to");
  // Keep uncommon orderings (including relevance and its anchor) intact until
  // the user explicitly chooses a different ordering.
  if (!Array.from(select.options).some((option) => option.value === current)) {
    select.add(new Option(current === "match" ? "Best match" : `Current order: ${current}`, current));
  }
  select.value = current;
  select.addEventListener("change", () => {
    const params = new URLSearchParams(window.location.search);
    if (select.value) params.set("sort", select.value);
    else params.delete("sort");
    // Only the initial custom ordering can use relevance. The built-in
    // choices cannot carry an anchor, which the server would reject.
    if (select.value === current && anchor) params.set("relative_to", anchor);
    else params.delete("relative_to");
    updateBrowserQuery(params);
    dispatchFiltersChange(window, { mode: "search", params });
  });
};
