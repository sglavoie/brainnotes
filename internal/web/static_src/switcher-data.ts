import { normalizeSearchResults, searchResultPath } from "./api-search";
import { fetchSavedViews } from "./saved-views-requests";
import { savedViewHref } from "./saved-views-model";

export type SwitcherEntry = { label: string; detail: string; path?: string; action?: string };
const recentKey = (): string => `bn.switcher.recent:${document.documentElement.dataset.vaultId}`;

export const recentEntries = (): SwitcherEntry[] => {
  try {
    const parsed: unknown = JSON.parse(sessionStorage.getItem(recentKey()) || "[]");
    if (!Array.isArray(parsed)) return [];
    return parsed.filter((entry): entry is SwitcherEntry => typeof entry?.label === "string" && typeof entry?.path === "string" && entry.path.startsWith("/n/")).slice(0, 8);
  } catch { return []; }
};

export const rememberCurrentNote = (): void => {
  const title = document.querySelector(".reading-title")?.textContent?.trim();
  if (!title || !window.location.pathname.startsWith("/n/")) return;
  const path = window.location.pathname;
  const entries = [{ label: title, path, detail: "Recent note" }, ...recentEntries().filter((entry) => entry.path?.toLocaleLowerCase() !== path.toLocaleLowerCase())].slice(0, 8);
  try { sessionStorage.setItem(recentKey(), JSON.stringify(entries)); } catch { /* Optional history. */ }
};

export const workspaceEntries = (): SwitcherEntry[] => {
  const entries: SwitcherEntry[] = [
    { label: "Daily Brief", path: "/", detail: "Go to" },
    { label: "Library", path: "/library", detail: "Go to" },
    { label: "Pinned notes", path: "/pinned", detail: "Go to" },
    { label: "Graph", path: "/graph", detail: "Go to" },
  ];
  if (document.documentElement.dataset.readOnly !== "true") entries.push(
    { label: "Capture a thought", action: "capture", detail: "Action" },
    { label: "Create a note", path: "/new", detail: "Action" },
  );
  if (document.querySelector("[data-reading-mode]")) entries.push({ label: "Toggle quiet reading", action: "reading", detail: "Action" });
  return entries;
};

export const savedViewEntries = async (): Promise<SwitcherEntry[]> => {
  const result = await fetchSavedViews(fetch);
  if (!result.ok) throw new Error("Saved views unavailable.");
  return result.result.views.filter((view) => view.enabled).map((view) => ({ label: view.name, detail: "Saved view", path: savedViewHref(view, "/library") }));
};

export const searchEntries = async (query: string, signal: AbortSignal): Promise<SwitcherEntry[]> => {
  const response = await fetch(`/api/search?${new URLSearchParams({ q: query, limit: "8" })}`, { signal });
  if (!response.ok) throw new Error("Search unavailable. Try again or open Library.");
  return normalizeSearchResults(await response.json()).map((note) => ({ label: note.title || note.key, detail: note.folder || "Note", path: searchResultPath(note) }));
};
