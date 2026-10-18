import { normalizeSearchResults } from "./api";
import type { FetchLike } from "./overview-search-model";

type SavedSelection = { query: string; keys: string[] };
type RestoredSelection = { targets: Map<string, string>; positions: Map<string, number> };

// Session storage isolates navigation state to this tab. Only keys are kept;
// wikilinks and membership must come from the current server response.
export class LibrarySelectionStore {
  private readonly key: string;
  private pending: SavedSelection | null = null;
  private query = "";

  constructor(vault: string) {
    this.key = vault ? `bn.library-selection.v1:${vault}` : "";
    try {
      const saved: SavedSelection = JSON.parse(sessionStorage.getItem(this.key) || "null");
      if (this.key && saved && typeof saved.query === "string" && Array.isArray(saved.keys) &&
          saved.keys.every((key) => typeof key === "string" && key.length > 0)) this.pending = saved;
    } catch { /* Selection still works when browser storage is unavailable. */ }
  }

  prepare(query: string): void {
    this.query = query;
    if (this.pending?.query !== query) this.pending = null;
  }

  async restore(fetcher: FetchLike, signal: AbortSignal): Promise<RestoredSelection | null> {
    if (!this.pending) return null;
    const keys = this.pending.keys;
    const found = new Map<string, string>();
    const positions = new Map<string, number>();
    for (let start = 0; start < keys.length; start += 50) {
      const url = new URL(this.query, window.location.origin);
      for (const key of keys.slice(start, start + 50)) url.searchParams.append("selected", key);
      const response = await fetcher(url.pathname + url.search, { signal, headers: { Accept: "application/json" } });
      if (!response.ok) throw new Error("Selection could not be checked.");
      const payload: unknown = await response.json();
      if (!Array.isArray(payload)) throw new Error("Selection could not be checked.");
      for (const result of normalizeSearchResults(payload)) {
        found.set(result.key, result.wikilink);
        positions.set(result.key, (result.position || 1) - 1);
      }
    }
    return { targets: found, positions };
  }

  restored(): void { this.pending = null; }

  save(selected: ReadonlyMap<string, string>): void {
    if (!this.key || !this.query || this.pending) return;
    try {
      if (selected.size) sessionStorage.setItem(this.key, JSON.stringify({ query: this.query, keys: [...selected.keys()] }));
      else sessionStorage.removeItem(this.key);
    } catch { /* A denied or full store must not interfere with selection. */ }
  }
}
