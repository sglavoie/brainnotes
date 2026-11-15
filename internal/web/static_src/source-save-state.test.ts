// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, mount, deferred, saveSuccess, saveFailure, LOADED } from "./source-editor-controller.test-helpers";
import type { SourceSaveResponse } from "./api";

afterEach(() => { cleanup(); vi.useRealTimers(); });

it("updates elapsed time without changing the live label and stops on disposal", async () => {
  vi.useFakeTimers();
  const h = mount();
  await h.controller.load();
  expect(h.saveState()).toBe("Saved");
  h.surface().type("new words");
  await h.controller.save();
  const label = document.querySelector("[data-save-label]")!;
  const node = label.firstChild;
  expect(h.saveState()).toBe("Saved just now");
  vi.advanceTimersByTime(120000);
  expect(h.saveState()).toBe("Saved 2m ago");
  expect(label.firstChild).toBe(node);
  expect(document.querySelector("[data-save-age]")?.getAttribute("aria-live")).toBe("off");
  h.controller.destroy();
  vi.advanceTimersByTime(60000);
  expect(h.saveState()).toBe("Saved 2m ago");
});

it("keeps newer edits unsaved when the submitted version succeeds", async () => {
  const pending = deferred<SourceSaveResponse>();
  const h = mount({ save: vi.fn().mockReturnValue(pending.promise) });
  await h.controller.load();
  h.surface().type("submitted");
  const saving = h.controller.save();
  expect(h.saveState()).toBe("Saving…");
  expect(h.saveButton().getAttribute("aria-busy")).toBe("true");
  h.surface().type("newer");
  pending.resolve(saveSuccess());
  await saving;
  expect(h.saveState()).toBe("Unsaved changes");
  expect(h.saveButton().disabled).toBe(false);
  expect(h.saveButton().getAttribute("aria-busy")).toBe("false");
});

it("keeps uncertainty visible even after undo and keeps the safe retry enabled", async () => {
  const h = mount({ save: vi.fn().mockRejectedValue(new Error("offline")) });
  await h.controller.load();
  h.surface().type("newer");
  await h.controller.save();
  h.surface().type(LOADED);
  expect(h.saveState()).toBe("Save unconfirmed");
  expect(h.saveButton().disabled).toBe(false);
  expect(h.status()).toContain("Press Save to retry safely");
});

it("keeps failed and conflicting writes unsaved with their recovery message", async () => {
  const h = mount({ save: vi.fn().mockResolvedValueOnce(saveFailure({ ok: false, code: "invalid", message: "Invalid source" }))
    .mockResolvedValueOnce(saveFailure({ ok: false, code: "conflict", message: "Changed on disk", revision: "r9", content: "theirs" })) });
  await h.controller.load();
  h.surface().type("mine");
  await h.controller.save();
  expect(h.saveState()).toBe("Unsaved changes");
  expect(h.status()).toBe("Invalid source");
  await h.controller.save();
  expect(h.saveState()).toBe("Unsaved changes");
  expect(h.status()).toBe("Changed on disk");
  expect(h.conflict().hidden).toBe(false);
});
