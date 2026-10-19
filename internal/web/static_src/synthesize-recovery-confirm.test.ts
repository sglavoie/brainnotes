// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { confirmAction } from "./action-confirm";
import { memoryStorage, query, routedFetch, start } from "./synthesize.test-helpers";
import { readSynthesisState, synthesisStorageKey, writeSynthesisState } from "./synthesize-storage";

vi.mock("./action-confirm", async (original) => ({
  ...await original<typeof import("./action-confirm")>(),
  confirmAction: vi.fn(async () => false),
}));

const stored = { sources: [], title: "Earlier research", folder: "", purpose: "synthesis", prompt: "", draft: "Keep my words", candidate: "" };

afterEach(() => {
  document.body.innerHTML = "";
  history.replaceState(null, "", "/");
  vi.mocked(confirmAction).mockClear();
});

const startWithSavedCopy = async (confirm?: (message: string, returnFocus?: Element | null) => boolean) => {
  const storage = memoryStorage();
  const legacy = synthesisStorageKey("vault-1", "anchor");
  writeSynthesisState(storage, legacy, stored);
  await start({ storage, fetch: routedFetch(), draftID: "fresh", confirm });
  const [restore, remove] = Array.from(document.querySelectorAll<HTMLButtonElement>(".synthesize-recovery-row button"));
  return { storage, legacy, restore, remove };
};

it("describes restoring and deleting saved copies in their own dialogs and returns focus to the clicked button", async () => {
  const { storage, legacy, restore, remove } = await startWithSavedCopy();

  restore.click();
  await vi.waitFor(() => expect(confirmAction).toHaveBeenCalledTimes(1));
  expect(vi.mocked(confirmAction).mock.calls[0][0]).toMatchObject({
    title: "Restore saved work?", confirmLabel: "Restore",
  });
  expect(vi.mocked(confirmAction).mock.calls[0][0].danger).toBeFalsy();
  expect(vi.mocked(confirmAction).mock.calls[0][2]).toBe(restore);

  remove.click();
  await vi.waitFor(() => expect(confirmAction).toHaveBeenCalledTimes(2));
  expect(vi.mocked(confirmAction).mock.calls[1][0]).toMatchObject({
    title: "Delete saved copy?", confirmLabel: "Delete", danger: true,
  });
  expect(vi.mocked(confirmAction).mock.calls[1][2]).toBe(remove);
  expect(readSynthesisState(storage, legacy)?.draft).toBe(stored.draft);
  expect(query<HTMLTextAreaElement>("[data-synthesize-draft]").value).toBe("");
});

it("passes the description and the clicked button to an injected confirmer", async () => {
  const confirm = vi.fn(() => true);
  const { storage, legacy, remove } = await startWithSavedCopy(confirm);
  remove.click();
  await vi.waitFor(() => expect(readSynthesisState(storage, legacy)).toBeNull());
  expect(confirm).toHaveBeenCalledWith("This cannot be undone. Open workbenches keep their current text.", remove);
  expect(confirmAction).not.toHaveBeenCalled();
});
