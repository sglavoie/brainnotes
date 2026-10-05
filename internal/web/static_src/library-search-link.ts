import { browserQueryChangeEventName, buildLibrarySearchURL, readPageQuery } from "./filters";

export const initLibrarySearchLink = (): void => {
  const button = document.querySelector<HTMLButtonElement>("[data-copy-search-link]");
  if (!button) return;
  const refresh = (): void => {
    // Copy search state, without the result-position anchor or navigation state.
    const text = new URL(buildLibrarySearchURL(readPageQuery(), true), window.location.origin).href;
    if (button.dataset.copyText === text) return;
    button.dataset.copyText = text;
    const root = button.closest("[data-copy-controls]");
    root?.querySelector("[data-copy-fallback]")?.remove();
    const status = root?.querySelector("[data-copy-status]");
    if (status) status.textContent = "";
  };
  refresh();
  button.hidden = false;
  button.addEventListener("click", refresh);
  window.addEventListener(browserQueryChangeEventName, refresh);
  window.addEventListener("popstate", refresh);
};
