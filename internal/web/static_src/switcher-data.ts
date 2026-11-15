import { readWorkspaceMemory, writeWorkspaceMemory } from "./workspace-memory";
import { searchExcerptText } from "./text-highlight";
import { normalizeSearchResults, searchResultPath } from "./api-search";
import { fetchSavedViews } from "./saved-views-requests";
import { savedViewHref } from "./saved-views-model";

// click names a control already on the page; choosing the entry activates it,
// so the switcher never grows a second implementation of an action. reveal
// opens the menu that control lives in first, for actions that report there.
export type SwitcherEntry = { label: string; detail: string; excerpt?: string; path?: string; click?: string; reveal?: boolean };

export const recentEntries = (): SwitcherEntry[] => {
  try {
    const parsed: unknown = JSON.parse(readWorkspaceMemory("switcher.recent") || "[]");
    if (!Array.isArray(parsed)) return [];
    return parsed.filter((entry): entry is SwitcherEntry => typeof entry?.label === "string" && typeof entry?.path === "string" && entry.path.startsWith("/n/")).slice(0, 8);
  } catch { return []; }
};

export const rememberCurrentNote = (): void => {
  const title = document.querySelector(".reading-title")?.textContent?.trim();
  if (!title || !window.location.pathname.startsWith("/n/")) return;
  const path = window.location.pathname;
  const entries = [{ label: title, path, detail: "Recent note" }, ...recentEntries().filter((entry) => entry.path?.toLocaleLowerCase() !== path.toLocaleLowerCase())].slice(0, 8);
  try { writeWorkspaceMemory("switcher.recent", JSON.stringify(entries)); } catch { /* Optional history. */ }
};

const readOnly = (): boolean => document.documentElement.dataset.readOnly === "true";
const destination = (label: string, path: string): SwitcherEntry => ({ label, path, detail: "Go to" });

// An action is offered only while its control exists and is usable, so the
// list is always exactly what this page and this serving mode can do.
const pageAction = (label: string, click: string, reveal = false): SwitcherEntry[] => {
  const control = document.querySelector<HTMLElement>(click);
  if (!control || control.hidden || (control instanceof HTMLButtonElement && control.disabled)) return [];
  if (control instanceof HTMLAnchorElement) return [{ label, detail: "Action", path: control.pathname + control.search }];
  return [{ label, detail: "Action", click, reveal }];
};

const noteActions = (): SwitcherEntry[] => {
  const pin = document.querySelector("[data-pin-button]")?.textContent?.trim();
  return [
    ...pageAction("Edit this note", "[data-source-editor-launch]"),
    ...(pin ? pageAction(`${pin} this note`, "[data-pin-button]") : []),
    ...pageAction("Toggle quiet reading", "[data-reading-mode]"),
    ...pageAction("Copy wikilink to this note", '[data-copy-label="Wikilink"]', true),
    ...pageAction("Copy this note’s URL", "[data-copy-note-url]", true),
    ...pageAction("Open this note in external editor", "[data-open-button]", true),
    ...pageAction("History for this note", "[data-note-history]"),
  ];
};

export const workspaceEntries = (): SwitcherEntry[] => [
  destination("Daily Brief", "/"),
  destination("Library", "/library"),
  ...(readOnly() ? [] : [destination("Threads", "/threads")]),
  destination("Pinned notes", "/pinned"),
  destination("Graph", "/graph"),
  destination("Properties", "/properties"),
  destination("Health", "/health"),
  ...(readOnly() ? [] : [destination("Settings", "/settings"), destination("Sync", "/sync"), destination("History", "/history")]),
  ...(readOnly() ? [] : [...pageAction("Capture a thought", "[data-capture-launch]"), { label: "Create a note", path: "/new", detail: "Action" }]),
  ...noteActions(),
  ...pageAction("Keyboard shortcuts", "[data-shortcuts-open]"),
];

export const savedViewEntries = async (): Promise<SwitcherEntry[]> => {
  const result = await fetchSavedViews(fetch);
  if (!result.ok) throw new Error("Saved views unavailable.");
  return result.result.views.filter((view) => view.enabled).map((view) => ({ label: view.name, detail: "Saved view", path: savedViewHref(view, "/library") }));
};

// Offered only when the exact search found nothing: the titles a misspelled
// or half-remembered search most likely meant.
export const closestEntries = async (query: string, signal: AbortSignal): Promise<SwitcherEntry[]> => {
  const response = await fetch(`/api/search/suggest?${new URLSearchParams({ q: query })}`, { signal });
  if (!response.ok) return [];
  return normalizeSearchResults(await response.json()).map((note) => ({ label: note.title || note.key, detail: note.folder ? `Close match · ${note.folder}` : "Close match", path: searchResultPath(note) }));
};

export const searchEntries = async (query: string, signal: AbortSignal): Promise<SwitcherEntry[]> => {
  const response = await fetch(`/api/search?${new URLSearchParams({ q: query, limit: "8" })}`, { signal });
  if (!response.ok) throw new Error("Search unavailable. Try again or open Library.");
  return normalizeSearchResults(await response.json()).map((note) => ({ label: note.title || note.key, detail: note.folder || "Note", excerpt: searchExcerptText(note.snippet), path: searchResultPath(note) }));
};


// Query actions share the result selection model but are not matching results.
export const queryEntries = (query: string): SwitcherEntry[] => {
  if (!query) return [];
  const library = { label: "Search in Library", detail: "Action", path: `/library?${new URLSearchParams({ q: query })}` };
  if (readOnly()) return [library];
  return [{ label: `Create “${query}”`, detail: "Action", path: `/new?${new URLSearchParams({ title: query })}` }, library];
};
