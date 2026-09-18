import { type SearchResult } from "./api";

export const searchResult = (overrides: Partial<SearchResult> = {}): SearchResult => ({
  key: "folder/note.md",
  path: "/n/folder/note.md",
  title: "Note Title",
  wikilink: "folder/note",
  folder: "folder",
  matched_fields: ["title"],
  issues: [],
  snippet: "Snippet text",
  pinned: false,
  ...overrides,
});

export const setURL = (url: string): void => {
  window.history.replaceState(null, "", url);
};
