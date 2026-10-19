// @vitest-environment jsdom

import { afterEach, expect, it, vi } from "vitest";

import { initSynthesize } from "./synthesize";
import {
  addCandidate,
  deferredResponse,
  identity,
  jsonResponse,
  memoryStorage,
  mount,
  query,
  relatedPayload,
  routedFetch,
  selectedKeys,
  settle,
  start,
  typeInto,
  unclearableStorage,
} from "./synthesize.test-helpers";
import { SYNTHESIS_STORAGE_VERSION } from "./synthesize-storage";

const KEY = "bn.synthesis-draft.[\"vault-1\",\"anchor\"].test";

const saved = (sources: string[]): string => JSON.stringify({
  version: SYNTHESIS_STORAGE_VERSION,
  sources,
  title: "Saved title",
  folder: "Synthesis",
  purpose: "synthesis",
  prompt: "",
  draft: "saved draft",
});

const storedSources = (storage: ReturnType<typeof memoryStorage>): string[] =>
  JSON.parse(String(storage.entries.get(KEY))).sources;

const resolved = (source: string): Response =>
  jsonResponse({ ...relatedPayload("balanced", []), source: identity(source) });

afterEach(() => {
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

it("keeps saved sources the vault could not be reached for and restores them on retry", async () => {
  const storage = memoryStorage();
  storage.entries.set(KEY, saved(["one", "two"]));
  let online = false;
  const fetcher = routedFetch({
    related: (url) => {
      const source = url.searchParams.get("source") || "";
      if (url.searchParams.get("limit") !== "1") return jsonResponse(relatedPayload("balanced", []));
      if (source === "two" && !online) throw new TypeError("Failed to fetch");
      return resolved(source);
    },
  });
  await start({ fetch: fetcher, storage, clipboard: null });

  expect(selectedKeys()).toEqual(["anchor", "one"]);
  const warning = query<HTMLElement>("[data-synthesize-warning]");
  expect(warning.textContent).toContain("Could not reach the vault to restore 1 saved note");
  expect(warning.textContent).not.toContain("no longer in this vault");
  expect(storedSources(storage)).toEqual(["one", "two"]);

  // An edit made before the retry still saves the unreachable key.
  typeInto("[data-synthesize-draft]", "edited");
  expect(storedSources(storage)).toEqual(["one", "two"]);

  online = true;
  query<HTMLButtonElement>("[data-synthesize-warning-action]").click();
  await vi.waitFor(() => expect(selectedKeys()).toEqual(["anchor", "one", "two"]));
  expect(warning.hidden).toBe(true);
  expect(storedSources(storage)).toEqual(["one", "two"]);
});

it("treats a server error as unreachable rather than as a deleted note", async () => {
  const storage = memoryStorage();
  storage.entries.set(KEY, saved(["one"]));
  const fetcher = routedFetch({
    related: (url) => url.searchParams.get("limit") === "1"
      ? jsonResponse({ ok: false, code: "internal", message: "restarting" }, 503)
      : jsonResponse(relatedPayload("balanced", [])),
  });
  await start({ fetch: fetcher, storage, clipboard: null });

  expect(selectedKeys()).toEqual(["anchor"]);
  expect(query<HTMLElement>("[data-synthesize-warning]").textContent).toContain("Could not reach the vault");
  expect(storedSources(storage)).toEqual(["one"]);
});

it("keeps a source added while the saved selection is still resolving", async () => {
  const storage = memoryStorage();
  storage.entries.set(KEY, saved(["one"]));
  const pending = deferredResponse();
  const fetcher = routedFetch({
    related: (url) => url.searchParams.get("limit") === "1"
      ? pending.promise
      : jsonResponse(relatedPayload("balanced", [])),
    search: () => jsonResponse([{ key: "late", path: "/n/late", title: "Late", snippet: "hit" }]),
  });
  mount();
  const controller = initSynthesize({
    draftID: "test", confirm: () => true, redirect: () => undefined, fetch: fetcher, storage, clipboard: null,
  });
  query<HTMLInputElement>("[data-synthesize-search-input]").value = "late";
  query<HTMLFormElement>("[data-synthesize-search-form]").dispatchEvent(
    new Event("submit", { bubbles: true, cancelable: true })
  );
  await vi.waitFor(() => {
    expect(document.querySelector("[data-synthesize-add='late']")).not.toBeNull();
  });
  addCandidate("late");

  pending.release(resolved("one"));
  await controller?.ready;
  await settle();
  expect(selectedKeys()).toEqual(["anchor", "one", "late"]);
  expect(storedSources(storage)).toEqual(["one", "late"]);
});

it("keeps offering discard while the browser refuses to delete the saved copy", async () => {
  await start({ fetch: routedFetch(), storage: unclearableStorage(), clipboard: null, confirm: () => true });
  typeInto("[data-synthesize-draft]", "words");
  const discard = query<HTMLButtonElement>("[data-synthesize-discard]");
  discard.click();
  await vi.waitFor(() => expect(query<HTMLElement>("[data-synthesize-warning]").hidden).toBe(false));
  expect(discard.hidden).toBe(false);
});
