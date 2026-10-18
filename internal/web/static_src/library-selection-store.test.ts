// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { LibrarySelectionStore } from "./library-selection-store";
import { searchResult } from "./search-test-helpers";

afterEach(() => sessionStorage.clear());

it("restores only current matches with current wikilinks, isolated by vault and query", async () => {
  const first = new LibrarySelectionStore("vault-a");
  first.prepare("/api/search?q=note&limit=75");
  first.save(new Map([["one", "Old"], ["gone", "Gone"], ["two", "Two"]]));
  const store = new LibrarySelectionStore("vault-a");
  store.prepare("/api/search?q=note&limit=75");
  const fetcher = vi.fn<typeof fetch>(async () => new Response(JSON.stringify([
    searchResult({ key: "one", wikilink: "Current" }), searchResult({ key: "two", wikilink: "Two" }),
  ])));
  const restored = await store.restore(fetcher, new AbortController().signal);
  expect([...restored!.targets]).toEqual([["one", "Current"], ["two", "Two"]]);
  expect(String(fetcher.mock.calls[0]?.[0])).toContain("selected=gone");
  const other = new LibrarySelectionStore("vault-b");
  other.prepare("/api/search?q=note&limit=75");
  expect(await other.restore(fetcher, new AbortController().signal)).toBeNull();
  store.prepare("/api/search?q=different");
  expect(await store.restore(fetcher, new AbortController().signal)).toBeNull();
});

it("keeps a selection pending after an unavailable check and never persists a receipt", async () => {
  const store = new LibrarySelectionStore("vault");
  store.prepare("/api/search?q=x");
  store.save(new Map([["one", "One"]]));
  const loaded = new LibrarySelectionStore("vault");
  loaded.prepare("/api/search?q=x");
  const signal = new AbortController().signal;
  await expect(loaded.restore(async () => new Response("", { status: 503 }), signal)).rejects.toThrow();
  loaded.save(new Map());
  expect(sessionStorage.getItem("bn.library-selection.v1:vault")).toBe('{"query":"/api/search?q=x","keys":["one"]}');
  loaded.restored();
  loaded.save(new Map());
  expect(sessionStorage.getItem("bn.library-selection.v1:vault")).toBeNull();
});
