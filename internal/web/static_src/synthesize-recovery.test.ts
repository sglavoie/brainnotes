// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { memoryStorage, query, routedFetch, start, typeInto } from "./synthesize.test-helpers";
import { readSynthesisState, synthesisStorageKey, writeSynthesisState } from "./synthesize-storage";
import { synthesisRecoveryKeys } from "./synthesize-recovery";

const stored = { sources: [], title: "Earlier research", folder: "", purpose: "synthesis", prompt: "", draft: "Keep my words", candidate: "" };

afterEach(() => {
  document.body.innerHTML = "";
  history.replaceState(null, "", "/");
  vi.restoreAllMocks();
});

it("keeps different workbenches independent and scopes recovery to this vault and anchor", async () => {
  const storage = memoryStorage();
  await start({ storage, fetch: routedFetch(), draftID: "first" });
  typeInto("[data-synthesize-draft]", "First workbench");
  await start({ storage, fetch: routedFetch(), draftID: "second" });
  typeInto("[data-synthesize-draft]", "Second workbench");
  const first = synthesisStorageKey("vault-1", "anchor", "first");
  const second = synthesisStorageKey("vault-1", "anchor", "second");
  writeSynthesisState(storage, synthesisStorageKey("other", "anchor", "third"), stored);
  writeSynthesisState(storage, synthesisStorageKey("vault-1", "elsewhere", "fourth"), stored);
  expect(readSynthesisState(storage, first)?.draft).toBe("First workbench");
  expect(readSynthesisState(storage, second)?.draft).toBe("Second workbench");
  expect(synthesisRecoveryKeys(storage, "vault-1", "anchor", second)).toEqual([first]);
});

it("offers legacy work explicitly and restores a copy without destroying its original", async () => {
  const storage = memoryStorage();
  const legacy = synthesisStorageKey("vault-1", "anchor");
  writeSynthesisState(storage, legacy, stored);
  await start({ storage, fetch: routedFetch(), draftID: "fresh" });
  expect(query<HTMLTextAreaElement>("[data-synthesize-draft]").value).toBe("");
  query<HTMLButtonElement>(".synthesize-recovery-row button").click();
  await vi.waitFor(() => expect(query<HTMLTextAreaElement>("[data-synthesize-draft]").value).toBe(stored.draft));
  typeInto("[data-synthesize-draft]", "My new version");
  expect(readSynthesisState(storage, legacy)?.draft).toBe(stored.draft);
  expect(readSynthesisState(storage, synthesisStorageKey("vault-1", "anchor", "fresh"))?.draft).toBe("My new version");
});

it("forks a resumed copy into a new slot and never persists context or preview receipts", async () => {
  const storage = memoryStorage();
  const prior = synthesisStorageKey("vault-1", "anchor", "prior");
  writeSynthesisState(storage, prior, stored);
  history.replaceState(null, "", "/synthesize?source=anchor&resume=prior");
  await start({ storage, fetch: routedFetch(), draftID: "fresh" });
  expect(query<HTMLTextAreaElement>("[data-synthesize-draft]").value).toBe(stored.draft);
  expect(new URLSearchParams(location.search).get("resume")).toBe("fresh");
  expect(query<HTMLButtonElement>("[data-synthesize-apply]").disabled).toBe(true);
  typeInto("[data-synthesize-draft]", "Forked");
  expect(readSynthesisState(storage, prior)?.draft).toBe(stored.draft);
});
