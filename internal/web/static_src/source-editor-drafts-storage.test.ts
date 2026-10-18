// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import type { SourceSaveResponse } from "./api-types-source";
import { draftKey, memoryDraftStorage, seedDraft } from "./source-draft.test-helpers";
import { notFoundCode } from "./source-save";
import {
  cleanup,
  VAULT,
  saveSuccess,
  saveFailure,
  mount,
  tick,
  seedResume,
  KEY,
  EDITED,
} from "./source-editor-drafts.test-helpers";

afterEach(cleanup);

describe("a browser that will not keep a copy", () => {
  // Said before a single keystroke, not after the first failed write: somebody
  // typing into a browser with no safety net should learn that while the note is
  // still short enough to retype.
  it("warns before anything is typed and leaves the editor entirely usable", async () => {
    const harness = mount({ storage: null });

    expect(harness.draftStatus()).toContain("browser storage is unavailable");
    await harness.controller.load();
    harness.surface().type(EDITED);
    await tick();
    await harness.controller.save();

    expect(harness.status()).toContain("Saved to vault.");
    expect(harness.client.save).toHaveBeenCalledTimes(1);
    // And it is still said afterwards. Nothing in a load or a save makes this
    // browser start keeping copies, so the line that says so may not be cleared
    // by either — there is no later write to bring it back.
    expect(harness.draftStatus()).toContain("browser storage is unavailable");
  });

  it("says storage is full rather than pretending the text is held", async () => {
    const harness = mount({ storage: memoryDraftStorage(0) });
    await harness.controller.load();

    harness.surface().type(EDITED);
    await tick();

    expect(harness.draftStatus()).toContain("storage is full");
    expect(harness.elements.draftStatus?.classList.contains("is-error")).toBe(true);
    expect(harness.drafts()).toEqual([]);
  });

  // A restore hands text back and then writes it to this tab's slot, and the
  // sentence it puts on the line says only that the text is in the editor. When
  // that write is refused the person is looking at recovered text that is now
  // held nowhere at all, so the refusal has to be the last word rather than the
  // one the restore's own message covers over.
  it("says storage is full rather than reporting a restore it could not keep", async () => {
    const storage = memoryDraftStorage();
    seedResume(storage, { draftID: "tab-other" });
    const harness = mount({ storage, draftID: "tab-this" });
    await harness.controller.load();
    storage.capacity = 0;

    harness.elements.recoveryRestore?.click();

    expect(harness.surface().getValue()).toBe(EDITED);
    expect(harness.draftStatus()).toContain("storage is full");
    expect(harness.elements.draftStatus?.classList.contains("is-error")).toBe(true);
  });

  // The same full store, restoring this tab's own record — which is the far more
  // common reload. Here the write is an overwrite of a key that already exists,
  // which no browser refuses, so dropping the record first is what would turn a
  // restore that works into one that loses the text: refused for want of room,
  // the recovered text now in the editor and nowhere else.
  it("still keeps this tab's own restored text when there is no room for a new key", async () => {
    const storage = memoryDraftStorage();
    seedResume(storage, { draftID: "tab-earlier" });
    const harness = mount({ storage, draftID: "tab-earlier" });
    await harness.controller.load();
    storage.capacity = 0;

    harness.elements.recoveryRestore?.click();

    expect(harness.draftStatus()).not.toContain("storage is full");
    expect(harness.drafts()).toEqual([
      expect.objectContaining({ draftID: "tab-earlier", content: EDITED }),
    ]);
  });

  // The record a hold is standing on can go without this tab doing anything: a
  // store out of room evicts the oldest draft on the whole origin, and the tab
  // that loses its record is not the tab that wrote. A hold kept for a record
  // that is gone is the worst state here — nothing kept at all, under a line
  // saying the recovered text still is.
  it("starts keeping again when the record it was holding for is evicted", async () => {
    const storage = memoryDraftStorage();
    seedResume(storage, { draftID: "tab-earlier" });
    const harness = mount({ storage, draftID: "tab-earlier" });
    await harness.controller.load();

    harness.surface().type(`${EDITED}newer\n`);
    await tick();
    expect(harness.drafts()).toEqual([
      expect.objectContaining({ draftID: "tab-earlier", content: EDITED }),
    ]);

    // Another tab, on another note, out of room.
    storage.removeItem(draftKey(VAULT, KEY, "tab-earlier"));
    harness.surface().type(`${EDITED}newer still\n`);
    await tick();

    expect(harness.drafts()).toEqual([
      expect.objectContaining({ draftID: "tab-earlier", content: `${EDITED}newer still\n` }),
    ]);
    expect(harness.draftStatus()).toContain("kept in this browser");
  });
});

// The panel and a request are two things that overlap in time, and the panel is
// still clickable while one is in flight. Both of these are about what the
// answer does when it lands on a page that has moved underneath it.
describe("an offer standing over a request in flight", () => {
  // A restore is the editor's whole state in one press — the text, and for a
  // stale draft the revision it was written against. A save answering afterwards
  // adopts its own submitted text and its own new revision over the top, leaving
  // pre-save text on screen pinned to the post-save revision: the next save is
  // accepted, with no conflict anywhere, over the version this one just wrote.
  it("declines a restore while a save is in flight", async () => {
    const storage = memoryDraftStorage();
    seedResume(storage, { draftID: "tab-earlier", baseRevision: "r0" });
    let land: (response: SourceSaveResponse) => void = () => {};
    const harness = mount({
      storage,
      draftID: "tab-earlier",
      client: {
        save: vi.fn(
          () => new Promise<SourceSaveResponse>((resolve) => { land = resolve; })
        ),
      },
    });
    await harness.controller.load();
    expect(harness.recoveryKind()).toBe("stale");

    harness.surface().type(`${EDITED}mine\n`);
    const saving = harness.controller.save();
    harness.elements.recoveryRestore?.click();

    expect(harness.surface().getValue()).toBe(`${EDITED}mine\n`);
    expect(harness.recovery().hidden).toBe(false);

    land(saveSuccess({ revision: "r2" }));
    await saving;

    // What the save wrote, still on screen, and the question still standing —
    // now against the version this tab has just put in the vault.
    expect(harness.surface().getValue()).toBe(`${EDITED}mine\n`);
    expect(harness.recoveryKind()).toBe("stale");
  });

  // An offer coming down is not an answer. The editor-sourced offer is this
  // tab's own text, and a save that finally lands settles it — but the record
  // from the previous session is still in the slot, still unasked about, and
  // still the reason nothing new is being kept. A panel that simply went dark
  // would leave the line telling somebody to restore or discard something they
  // can no longer see, over a tab that keeps nothing for the rest of the session.
  it("asks the next question when a save takes its own offer down", async () => {
    const storage = memoryDraftStorage();
    seedResume(storage, { draftID: "tab-earlier" });
    const harness = mount({
      storage,
      draftID: "tab-earlier",
      client: {
        save: vi
          .fn()
          .mockResolvedValueOnce(saveFailure({ code: notFoundCode, message: "gone" }))
          .mockResolvedValue(saveSuccess({ revision: "r2" })),
      },
    });
    await harness.controller.load();
    harness.surface().type(`${EDITED}newer\n`);
    await harness.controller.save();
    expect(harness.recoveryKind()).toBe("missing");

    // The note is back — a peer sync, another tab — and this save lands.
    await harness.controller.save();

    expect(harness.recovery().hidden).toBe(false);
    expect(harness.elements.recoverySource?.textContent).toBe(EDITED);

    harness.elements.recoveryDiscard?.click();
    harness.surface().type(`${EDITED}newer and newer\n`);
    await tick();

    expect(harness.drafts()).toEqual([
      expect.objectContaining({ draftID: "tab-earlier", content: `${EDITED}newer and newer\n` }),
    ]);
  });
});

// A request that never answers is the ordinary case here rather than the exotic
// one: this server is usually a Raspberry Pi on somebody's home network, and a
// dropped connection or a restart mid-save leaves the editor saving until the
// tab is closed.
describe("an offer standing over a request that never answers", () => {
  // Restore and recreate are declined for the duration of a request. Discard is
  // not, and must not be: it is the only thing that ends a hold, so declining it
  // here would mean a tab that keeps nothing at all for the rest of the session
  // with the one button that could fix that inert. It re-pins nothing and puts
  // nothing on screen for the late answer to land on top of.
  it("still takes an answer to the offer while a save is in flight", async () => {
    const storage = memoryDraftStorage();
    seedResume(storage, { draftID: "tab-earlier" });
    const harness = mount({
      storage,
      draftID: "tab-earlier",
      client: { save: vi.fn(() => new Promise<SourceSaveResponse>(() => {})) },
    });
    await harness.controller.load();

    harness.surface().type(`${EDITED}mine\n`);
    void harness.controller.save();
    harness.elements.recoveryDiscard?.click();
    await tick();

    expect(harness.recovery().hidden).toBe(true);
    expect(harness.drafts()).toEqual([
      expect.objectContaining({ draftID: "tab-earlier", content: `${EDITED}mine\n` }),
    ]);
    expect(harness.draftStatus()).toContain("kept in this browser");
  });
});

// Two ways this tab's own slot stops being what the panel thinks it is: a write
// the browser refuses outright, and a record another tab takes away.
describe("a slot the browser cannot spare", () => {
  // Restoring another tab's draft writes it into this tab's slot and then drops
  // the record it came from — in that order, so the bytes are in two places for
  // an instant rather than in none. The order is not enough on its own: the
  // write can be refused, and dropping the source on the strength of one that
  // never happened leaves the restored text on screen and nowhere else.
  it("keeps the record it restored from when the browser refuses the copy", async () => {
    const storage = memoryDraftStorage();
    seedDraft(storage, {
      vaultID: VAULT,
      noteKey: "beta",
      draftID: "tab-elsewhere",
      content: "another note's unsaved text\n",
      updatedAt: 1_000,
    });
    seedResume(storage, { draftID: "tab-other", content: `${EDITED}theirs\n` });
    const harness = mount({ storage });
    await harness.controller.load();
    // Room for one record, and two on the origin: the store evicts the oldest
    // foreign draft to make space and is refused even so.
    storage.capacity = 1;

    harness.elements.recoveryRestore?.click();
    await tick();

    expect(harness.draftStatus()).toContain("storage is full");
    expect(harness.surface().getValue()).toBe(`${EDITED}theirs\n`);
    expect(harness.drafts()).toEqual([
      expect.objectContaining({ draftID: "tab-other", content: `${EDITED}theirs\n` }),
    ]);
  });

  // The hold protects this tab's own record while another tab's is asked about
  // first. When that record is evicted from under it, the writer takes the slot
  // for the live text — and the presenter must not be remembering its own answer
  // to the same question, or the next discard offers somebody back the words
  // they are looking at, with a Discard button pointed at their only copy.
  it("does not offer back the live text after the record it held for is evicted", async () => {
    const storage = memoryDraftStorage();
    seedResume(storage, { draftID: "tab-earlier", content: `${EDITED}mine\n`, updatedAt: 8_000 });
    seedResume(storage, { draftID: "tab-other", content: `${EDITED}theirs\n`, updatedAt: 9_000 });
    const harness = mount({ storage, draftID: "tab-earlier" });
    await harness.controller.load();
    expect(harness.elements.recoverySource?.textContent).toBe(`${EDITED}theirs\n`);

    storage.removeItem(draftKey(VAULT, KEY, "tab-earlier"));
    harness.surface().type(`${EDITED}live\n`);
    await tick();

    harness.elements.recoveryDiscard?.click();
    await tick();

    expect(harness.recovery().hidden).toBe(true);
    expect(harness.drafts()).toEqual([
      expect.objectContaining({ draftID: "tab-earlier", content: `${EDITED}live\n` }),
    ]);
  });

  // The same refusal as above, in the state that makes "is there anything in the
  // slot" the wrong question: this tab already had an unanswered record of its
  // own in it. A browser measures characters, so the overwrite can be refused
  // while the slot stays exactly as occupied as it was — and a slot that merely
  // holds *something* then reads as a write that landed, with the record the
  // text came from dropped on the strength of it.
  it("keeps the record it restored from when the refused slot still holds the old one", async () => {
    const storage = memoryDraftStorage();
    // Older than either of this note's records, so this is what the store gives
    // up when it makes room — and it is nowhere near enough room.
    seedDraft(storage, {
      vaultID: VAULT,
      noteKey: "beta",
      draftID: "tab-elsewhere",
      content: "another note's unsaved text\n",
      updatedAt: 1_000,
    });
    seedResume(storage, { draftID: "tab-earlier", content: `${EDITED}mine\n`, updatedAt: 8_000 });
    const theirs = `${EDITED}theirs\n${"and a good deal more of it\n".repeat(60)}`;
    seedResume(storage, { draftID: "tab-other", content: theirs, updatedAt: 9_000 });
    const harness = mount({ storage, draftID: "tab-earlier" });
    await harness.controller.load();
    expect(harness.elements.recoverySource?.textContent).toBe(theirs);
    // Not a character to spare.
    storage.maxBytes = storage.bytes();

    harness.elements.recoveryRestore?.click();
    await tick();

    expect(harness.draftStatus()).toContain("storage is full");
    expect(harness.surface().getValue()).toBe(theirs);
    // Both of this note's records exactly as they were: the restore wrote
    // nothing, so it may take nothing away.
    expect(harness.drafts()).toEqual([
      expect.objectContaining({ draftID: "tab-earlier", content: `${EDITED}mine\n` }),
      expect.objectContaining({ draftID: "tab-other", content: theirs }),
    ]);
  });
});
