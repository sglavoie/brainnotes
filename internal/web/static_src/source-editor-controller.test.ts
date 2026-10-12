// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import type { SourceClient } from "./source-save";
import type { SourceSaveResponse } from "./api";
import {
  cleanup,
  LOADED,
  mount,
  saveFailure,
  saveSuccess,
  deferred,
} from "./source-editor-controller.test-helpers";

afterEach(cleanup);

describe("loading", () => {
  it("opens Find in the edit pane without changing or saving the note", async () => {
    const harness = mount();
    expect(harness.elements.find?.disabled).toBe(true);
    await harness.controller.load();
    const openSearch = vi.spyOn(harness.surface(), "openSearch");
    harness.controller.panes.select("preview");
    harness.elements.find?.click();
    expect(harness.controller.panes.active()).toBe("edit");
    expect(openSearch).toHaveBeenCalledOnce();
    expect(harness.controller.dirty()).toBe(false);
    expect(harness.client.save).not.toHaveBeenCalled();
  });

  it("puts the note's bytes in the surface and starts clean", async () => {
    const harness = mount();
    await harness.controller.load();

    expect(harness.surface().getValue()).toBe(LOADED);
    expect(harness.controller.dirty()).toBe(false);
    expect(harness.saveButton().disabled).toBe(true);
    expect(harness.status()).toContain("Alpha.md");
    expect(document.title).toBe("Edit Alpha - bn serve");
  });

  it("renders a preview of the loaded source without waiting for a keystroke", async () => {
    const harness = mount();
    await harness.controller.load();
    await vi.waitFor(() => expect(harness.elements.preview?.innerHTML).toBe("<p>Alpha body.</p>"));
  });

  it("reports a refused load and mounts nothing", async () => {
    const harness = mount({
      read: vi.fn().mockRejectedValue(new Error("read-only replica")) as unknown as SourceClient["read"],
    });
    await harness.controller.load();

    expect(harness.status()).toContain("read-only replica");
    expect(harness.elements.status?.classList.contains("is-error")).toBe(true);
    expect(harness.saveButton().disabled).toBe(true);
  });
});

describe("dirtiness", () => {
  it("enables Save on an edit and disables it again when the text comes back", async () => {
    const harness = mount();
    await harness.controller.load();

    harness.surface().type(`${LOADED}more\n`);
    expect(harness.controller.dirty()).toBe(true);
    expect(harness.saveButton().disabled).toBe(false);
    expect(document.title).toBe("• Edit Alpha - bn serve");

    // Undoing back to the loaded text is clean, which a boolean flag set on the
    // first keystroke would get wrong.
    harness.surface().type(LOADED);
    expect(harness.controller.dirty()).toBe(false);
    expect(harness.saveButton().disabled).toBe(true);
    expect(document.title).toBe("Edit Alpha - bn serve");
  });

  // Undoing is also the natural reaction to a save that did not answer, and
  // there the text matching the vault proves nothing: those bytes may be on
  // disk. The button has to keep offering the one action that settles it, or it
  // says "nothing unsaved" over an attempt of unknown outcome and leaves
  // Ctrl/⌘+S — which does not consult it — as the only way through.
  it("keeps Save armed over an unanswered attempt the text was undone after", async () => {
    const save = vi.fn().mockRejectedValue(new Error("Failed to fetch"));
    const harness = mount({ save: save as unknown as SourceClient["save"] });
    await harness.controller.load();
    harness.surface().type("edited\n");

    await harness.controller.save();
    expect(harness.status()).toContain("press Save to retry");

    harness.surface().type(LOADED);
    expect(harness.controller.dirty()).toBe(false);
    expect(harness.saveButton().disabled).toBe(false);
    expect(document.title).toBe("• Edit Alpha - bn serve");

    harness.saveButton().click();
    await vi.waitFor(() => expect(save).toHaveBeenCalledTimes(2));
    expect(save.mock.calls[1][0].content).toBe(LOADED);
  });
});

describe("saving", () => {
  it("submits the loaded revision and re-pins to the one the server answers with", async () => {
    const harness = mount();
    await harness.controller.load();
    harness.surface().type("edited\n");

    await harness.controller.save();
    expect(harness.client.save.mock.calls[0][0]).toMatchObject({ content: "edited\n", revision: "r1" });
    expect(harness.controller.dirty()).toBe(false);
    expect(harness.status()).toBe("Saved Alpha.md.");
    expect(document.title).toBe("Edit Alpha - bn serve");

    harness.surface().type("edited twice\n");
    await harness.controller.save();
    expect(harness.client.save.mock.calls[1][0]).toMatchObject({ revision: "r2" });
  });

  it("does nothing while a save is already in flight", async () => {
    let release!: (value: SourceSaveResponse) => void;
    const answer = new Promise<SourceSaveResponse>((resolve) => {
      release = resolve;
    });
    const harness = mount({
      save: vi.fn().mockReturnValue(answer) as unknown as SourceClient["save"],
    });
    await harness.controller.load();
    harness.surface().type("edited\n");

    const first = harness.controller.save();
    await harness.controller.save();
    expect(harness.client.save).toHaveBeenCalledTimes(1);

    release(saveSuccess({ revision: "r2", message: "Saved." }));
    await first;
  });

  // The idempotency ledger keys on save_id, so a *new* attempt must carry a new
  // one — otherwise the second save would replay the first and silently write
  // nothing.
  it("mints a fresh save id for each answered attempt", async () => {
    const harness = mount();
    await harness.controller.load();

    harness.surface().type("one\n");
    await harness.controller.save();
    harness.surface().type("two\n");
    await harness.controller.save();

    const [first, second] = harness.client.save.mock.calls.map((call) => call[0].saveID);
    expect(first).not.toBe("");
    expect(second).not.toBe(first);
  });

  // The request never produced an answer, so whether the bytes landed is
  // unknown. Reusing the id is what makes the retry a replay rather than a
  // second write.
  it("reuses the save id after a request that never answered", async () => {
    const save = vi
      .fn()
      .mockRejectedValueOnce(new Error("Failed to fetch"))
      .mockResolvedValue({ ok: true, revision: "r2", message: "Saved." });
    const harness = mount({ save: save as unknown as SourceClient["save"] });
    await harness.controller.load();
    harness.surface().type("edited\n");

    await harness.controller.save();
    expect(harness.status()).toContain("press Save to retry");
    expect(harness.controller.dirty()).toBe(true);

    await harness.controller.save();
    expect(save.mock.calls[0][0].saveID).toBe(save.mock.calls[1][0].saveID);
  });
});

describe("a save the server does not accept", () => {
  it("refuses a note over the server's size limit before sending it", async () => {
    const harness = mount();
    await harness.controller.load();
    harness.surface().type("x".repeat(harness.config.maxBytes + 1));

    await harness.controller.save();
    expect(harness.client.save).not.toHaveBeenCalled();
    expect(harness.status()).toContain("over the");
    expect(harness.status()).toContain("Nothing was sent");
  });

  // A snapshot failure: the bytes are on disk and the content history does not
  // cover them. The revision has to move anyway, or the next save is planned
  // against one this save already replaced.
  it("re-pins after a save that landed but could not be snapshotted", async () => {
    const harness = mount({
      save: vi.fn().mockResolvedValue(
        saveFailure({
          ok: false,
          code: "snapshot_failed",
          message: "the change is on disk and further writes are blocked",
          revision: "r7",
          changed: true,
        })
      ) as unknown as SourceClient["save"],
    });
    await harness.controller.load();
    harness.surface().type("edited\n");

    await harness.controller.save();
    expect(harness.status()).toContain("further writes are blocked");
    expect(harness.elements.status?.classList.contains("is-error")).toBe(true);
    expect(harness.controller.dirty()).toBe(false);

    harness.surface().type("again\n");
    await harness.controller.save();
    expect(harness.client.save.mock.calls[1][0].revision).toBe("r7");
  });

  it("reports a plain refusal and keeps the text dirty", async () => {
    const harness = mount({
      save: vi.fn().mockResolvedValue(
        saveFailure({ ok: false, code: "invalid_request", message: "source: frontmatter is not valid YAML" })
      ) as unknown as SourceClient["save"],
    });
    await harness.controller.load();
    harness.surface().type("bad\n");

    await harness.controller.save();
    expect(harness.status()).toContain("not valid YAML");
    expect(harness.controller.dirty()).toBe(true);
  });
});

it("keeps the tab marked when typing continues during a save", async () => {
  const answer = deferred<SourceSaveResponse>();
  const save = vi.fn().mockReturnValueOnce(answer.promise).mockResolvedValue(saveSuccess());
  const harness = mount({ save });
  await harness.controller.load();
  harness.surface().type("first edit");
  const saving = harness.controller.save();
  harness.surface().type("second edit");
  answer.resolve(saveSuccess());
  await saving;
  expect(document.title).toBe("• Edit Alpha - bn serve");
  await harness.controller.save();
  expect(document.title).toBe("Edit Alpha - bn serve");
});

it("restores the title on teardown and ignores a late save failure", async () => {
  const answer = deferred<SourceSaveResponse>();
  const harness = mount({ save: vi.fn().mockReturnValue(answer.promise) });
  await harness.controller.load();
  harness.surface().type("edited");
  const saving = harness.controller.save();
  expect(document.title).toBe("• Edit Alpha - bn serve");
  harness.controller.destroy();
  expect(document.title).toBe("Edit Alpha - bn serve");
  document.title = "Another page";
  answer.resolve(saveFailure({ ok: false }));
  await saving;
  expect(document.title).toBe("Another page");
});

describe("the size limit", () => {
  // The limit is a byte count the server enforces, and the editor holds a
  // string. Measuring `length` would let a note of multi-byte characters pass
  // here and be refused there, which is the check failing at its one job.
  it("measures bytes rather than characters", async () => {
    const harness = mount();
    await harness.controller.load();
    // Each "é" is two bytes in UTF-8, so this is under the limit by character
    // count and over it by byte count.
    harness.surface().type("é".repeat(harness.config.maxBytes - 1));

    await harness.controller.save();
    expect(harness.client.save).not.toHaveBeenCalled();
    expect(harness.status()).toContain("Nothing was sent");
  });

  it("sends a note that is exactly the limit", async () => {
    const harness = mount();
    await harness.controller.load();
    harness.surface().type("x".repeat(harness.config.maxBytes));

    await harness.controller.save();
    expect(harness.client.save).toHaveBeenCalledTimes(1);
  });
});

describe("leaving the page", () => {
  const beforeUnload = (): Event => {
    const event = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(event);
    return event;
  };

  // The text exists only in this tab: nothing has been written, and there is no
  // draft anywhere to come back to.
  it("asks for the browser's prompt while there are unsaved bytes", async () => {
    const harness = mount();
    await harness.controller.load();
    harness.surface().type("edited\n");

    expect(beforeUnload().defaultPrevented).toBe(true);
  });

  it("says nothing when the editor matches the vault", async () => {
    const harness = mount();
    await harness.controller.load();

    expect(beforeUnload().defaultPrevented).toBe(false);

    harness.surface().type("edited\n");
    await harness.controller.save();
    expect(beforeUnload().defaultPrevented).toBe(false);
  });

  // destroy() unbinds the window listener as well as the document one, or a
  // torn-down editor keeps prompting about a page it no longer owns.
  it("stops asking once destroyed", async () => {
    const harness = mount();
    await harness.controller.load();
    harness.surface().type("edited\n");
    harness.controller.destroy();

    expect(beforeUnload().defaultPrevented).toBe(false);
  });
});

describe("controls", () => {
  it("runs a toolbar command against the current selection", async () => {
    const harness = mount();
    await harness.controller.load();
    harness.surface().type("word");

    const bold = document.querySelector<HTMLButtonElement>('[data-source-command="bold"]');
    harness.surface().applyEdit({ from: 0, to: 4, insert: "word", selectionFrom: 0, selectionTo: 4 });
    bold?.click();
    expect(harness.surface().getValue()).toBe("**word**");
  });

  // The delay is non-zero on purpose. With a zero debounce the scheduled render
  // has already happened by the time the tab is clicked, so the assertion holds
  // whether or not choosing the pane flushes anything — which is the whole
  // behaviour under test. A real delay makes the pending render still pending,
  // so the second call can only come from the flush.
  it("renders the pending text immediately when the preview tab is chosen", async () => {
    const harness = mount({}, 50);
    await harness.controller.load();
    await vi.waitFor(() => expect(harness.client.preview).toHaveBeenCalledTimes(1));

    harness.surface().type("typed but not yet debounced\n");
    expect(harness.client.preview).toHaveBeenCalledTimes(1);

    const previewTab = document.querySelector<HTMLButtonElement>('[data-source-tab="preview"]');
    previewTab?.click();

    expect(harness.client.preview).toHaveBeenCalledTimes(2);
    expect(harness.client.preview.mock.calls[1][0]).toBe("typed but not yet debounced\n");
    expect(harness.controller.panes.active()).toBe("preview");
    expect(previewTab?.getAttribute("aria-selected")).toBe("true");
    expect(document.querySelector(".source-editor-panes")?.getAttribute("data-source-active")).toBe("preview");
  });
});

// insertText is the door the attachment tracker writes a link through. What it
// owes is the behaviour of having typed the link: the selection is replaced, the
// cursor lands after it, and the editor knows it is dirty.
describe("insertText", () => {
  it("replaces the selection and leaves the cursor after what it inserted", async () => {
    const harness = mount();
    await harness.controller.load();
    harness.surface().type("before SELECTED after");
    harness.surface().select(7, 15);

    harness.controller.insertText("[a](b)");
    expect(harness.surface().getValue()).toBe("before [a](b) after");
    expect(harness.surface().getSelection()).toEqual({ from: 13, to: 13 });
  });

  // An inserted link that the editor does not count as an edit is one the draft
  // keeper never writes down and the Save button never lights up for: the note
  // would reference a staged file that no save ever commits.
  it("counts as an edit, so the draft keeper and the Save button hear it", async () => {
    const harness = mount();
    await harness.controller.load();
    expect(harness.controller.dirty()).toBe(false);

    harness.controller.insertText("![shot.png](attachments/shot.png)");
    expect(harness.controller.dirty()).toBe(true);
    expect(harness.saveButton().disabled).toBe(false);
  });

  // An upload can finish before the read does, or after a read that failed.
  it("does nothing on a page whose editor never mounted", () => {
    const harness = mount();
    expect(() => harness.controller.insertText("![a](b)")).not.toThrow();
  });
});
