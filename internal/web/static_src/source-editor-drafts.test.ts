// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import type { SourceSaveResponse } from "./api-types-source";
import { draftKey, memoryDraftStorage } from "./source-draft.test-helpers";
import {
  cleanup,
  VAULT,
  saveSuccess,
  saveFailure,
  mount,
  tick,
  seedResume,
  KEY,
  LOADED,
  EDITED,
} from "./source-editor-drafts.test-helpers";

afterEach(cleanup);

describe("keeping unsaved text", () => {
  it("keeps typed text under this tab's key and sends nothing", async () => {
    const harness = mount();
    await harness.controller.load();

    harness.surface().type(EDITED);
    await tick();

    expect(harness.storage.keys()).toEqual([draftKey(VAULT, KEY, "tab-this")]);
    expect(harness.drafts()[0]).toMatchObject({
      relPath: "Alpha.md",
      baseRevision: "r1",
      content: EDITED,
      draftID: "tab-this",
    });
    expect(harness.client.save).not.toHaveBeenCalled();
  });

  // Undoing an edit back to the vault's text is somebody saying they no longer
  // want it. A copy left behind would be offered back to them tomorrow.
  it("drops the copy when the text comes back to what the vault holds", async () => {
    const harness = mount();
    await harness.controller.load();

    harness.surface().type(EDITED);
    await tick();
    expect(harness.drafts()).toHaveLength(1);

    harness.surface().type(LOADED);
    await tick();

    expect(harness.drafts()).toEqual([]);
    expect(harness.draftStatus()).toBe("");
  });

  it("drops the copy once the vault holds the text", async () => {
    const harness = mount();
    await harness.controller.load();

    harness.surface().type(EDITED);
    await tick();
    await harness.controller.save();

    expect(harness.drafts()).toEqual([]);
    expect(harness.draftStatus()).toBe("");
    expect(harness.status()).toContain("Saved Alpha.md.");
  });

  // The interruption this feature is most often answering: the request is in
  // flight and the server, the network or the tab goes away. The last keystrokes
  // have to be in storage by then rather than still sitting in the debounce.
  it("writes the pending keystrokes before the request leaves", async () => {
    const gate: { answer?: (response: SourceSaveResponse) => void } = {};
    const harness = mount({
      client: {
        save: vi.fn(
          () =>
            new Promise<SourceSaveResponse>((resolve) => {
              gate.answer = resolve;
            })
        ),
      },
    });
    await harness.controller.load();

    harness.surface().type(EDITED);
    // Deliberately no tick: the debounce is still holding the text when Save is
    // pressed, which is the ordinary case for Ctrl/⌘ + S mid-sentence.
    const saving = harness.controller.save();
    expect(harness.drafts()[0]?.content).toBe(EDITED);

    gate.answer?.(saveSuccess());
    await saving;
    expect(harness.drafts()).toEqual([]);
  });

  // The completion condition in one test: local storage is never mislabeled as a
  // Pi save. The two lines are separate elements with separate subjects, and the
  // draft line's own sentence says where the text is not.
  it("says what it is keeping without ever calling it a save", async () => {
    const harness = mount();
    await harness.controller.load();

    harness.surface().type(EDITED);
    await tick();

    expect(harness.draftStatus()).toContain("kept in this browser only");
    expect(harness.draftStatus()).toContain("not in the vault until you save");
    expect(harness.draftStatus()).not.toMatch(/\bsaved\b/i);
    // The vault's own line is untouched by any of it.
    expect(harness.status()).toContain("Alpha.md loaded");
    expect(harness.elements.draftStatus).not.toBe(harness.elements.status);
  });
});

describe("offering kept text back", () => {
  // The reload. The note has not moved, so putting the text back leaves the
  // person where they were — after they say so.
  it("offers a draft the last session left behind and submits nothing", async () => {
    const storage = memoryDraftStorage();
    seedResume(storage);
    const harness = mount({ storage });
    await harness.controller.load();

    expect(harness.recovery().hidden).toBe(false);
    expect(harness.recoveryKind()).toBe("resume");
    expect(harness.elements.recoverySource?.textContent).toBe(EDITED);
    expect(harness.elements.recoveryRestore?.hidden).toBe(false);
    expect(harness.elements.recoveryRecreate?.hidden).toBe(true);
    // The editor is showing the vault's bytes until somebody chooses otherwise.
    expect(harness.surface().getValue()).toBe(LOADED);
    expect(harness.client.save).not.toHaveBeenCalled();
    expect(harness.client.create).not.toHaveBeenCalled();
  });

  // The reload again, from the far more common angle: the same tab, which keeps
  // its identity across a reload through session storage. The record has to
  // still be in storage while the offer is up — everything on screen is one
  // more interruption away from being gone, and an offer nobody answered is not
  // an answer.
  it("leaves the record in storage until the offer is answered, reload after reload", async () => {
    const storage = memoryDraftStorage();
    seedResume(storage);
    const harness = mount({ storage, draftID: "tab-earlier" });
    await harness.controller.load();

    expect(harness.recovery().hidden).toBe(false);
    expect(harness.elements.recoveryRestore?.hidden).toBe(false);
    expect(harness.drafts()).toHaveLength(1);

    // The page goes away again with the question still on screen: the editor
    // holds exactly what the vault holds, which is not a reason to drop text
    // nobody has decided about.
    window.dispatchEvent(new Event("pagehide"));
    harness.controller.destroy();

    expect(harness.drafts()[0]).toMatchObject({ draftID: "tab-earlier", content: EDITED });
  });

});

// One slot per tab, and after a reload an unanswered offer is standing on it.
// Everything that would ordinarily write there — a keystroke, a save, the drop
// that follows text going back to what the vault holds — has to leave it alone
// until somebody has decided, because that record is the only copy of the text
// on the panel.
describe("the slot an unanswered offer is standing on", () => {
  // There is one slot per tab and an unanswered offer is standing on it. Text
  // typed on top of that offer cannot have the slot as well, and the recovered
  // text is the one that has already survived an interruption — so it keeps its
  // place, and the line says which of the two is being held.
  it("does not spend this tab's slot on new text while its own offer is unanswered", async () => {
    const storage = memoryDraftStorage();
    seedResume(storage);
    const harness = mount({ storage, draftID: "tab-earlier" });
    await harness.controller.load();

    harness.surface().type(`${EDITED}and something newer\n`);
    await tick();

    expect(harness.drafts()).toHaveLength(1);
    expect(harness.drafts()[0]).toMatchObject({ draftID: "tab-earlier", content: EDITED });
    expect(harness.draftStatus()).toContain("still being kept in this browser");
    expect(harness.draftStatus()).not.toMatch(/\bsaved\b/i);

    // And the moment the question is answered the slot is the new text's, with
    // no further keystroke needed to start it — the person who discards is
    // usually the person who has already typed their replacement.
    harness.elements.recoveryDiscard?.click();
    await tick();

    expect(harness.drafts()).toHaveLength(1);
    expect(harness.drafts()[0]).toMatchObject({
      draftID: "tab-earlier",
      content: `${EDITED}and something newer\n`,
    });
    // And the line says so. Crossing out of the hold un-says the sentence the
    // hold put there, or the first genuine keep after it would be swallowed as
    // something already announced and "the recovered text is still being kept,
    // so what you are typing now is not" would stay on screen over a slot that
    // now holds exactly what they are typing.
    expect(harness.draftStatus()).toContain("kept in this browser only");
  });

  // The same slot, taken the other way. A save is this editor's text reaching
  // the vault and says nothing about text somebody has not answered for, so the
  // drop that ordinarily follows one must not reach into the offer's record.
  it("leaves an unanswered offer's record alone when this editor saves", async () => {
    const storage = memoryDraftStorage();
    seedResume(storage);
    const harness = mount({ storage, draftID: "tab-earlier" });
    await harness.controller.load();

    harness.surface().type(`${EDITED}and something newer\n`);
    await harness.controller.save();

    expect(harness.client.save).toHaveBeenCalled();
    expect(harness.recovery().hidden).toBe(false);
    expect(harness.drafts()).toHaveLength(1);
    expect(harness.drafts()[0]).toMatchObject({ draftID: "tab-earlier", content: EDITED });
  });

  // The save landed and the tab never learned it — closed mid-request, lost
  // server, laptop shut. The record it left behind holds exactly what the vault
  // now holds, so there is nothing to ask about and nothing to keep it for: the
  // hold goes up as the record is read and has to come straight back down with
  // it, or a tab whose only crime was a save it never saw would quietly keep
  // nothing for the rest of its life.
  it("stops holding the slot for a record the vault turns out to hold already", async () => {
    const storage = memoryDraftStorage();
    seedResume(storage, { draftID: "tab-earlier", content: LOADED });
    const harness = mount({ storage, draftID: "tab-earlier" });
    await harness.controller.load();

    expect(harness.recovery().hidden).toBe(true);
    expect(harness.drafts()).toEqual([]);

    harness.surface().type(EDITED);
    await tick();

    expect(harness.drafts()).toHaveLength(1);
    expect(harness.drafts()[0]).toMatchObject({ draftID: "tab-earlier", content: EDITED });
    expect(harness.draftStatus()).toContain("kept in this browser only");
  });

  // The hold is about the slot, not about the panel: another tab's newer draft
  // is asked about first, and this tab's own record is underneath it the whole
  // time — clean editor, unload flush and all.
  it("keeps its own record through a question that is about another tab's", async () => {
    const storage = memoryDraftStorage();
    seedResume(storage, { draftID: "tab-earlier", content: `${EDITED}mine\n`, updatedAt: 8_000 });
    seedResume(storage, { draftID: "tab-other", content: `${EDITED}theirs\n`, updatedAt: 9_000 });
    const harness = mount({ storage, draftID: "tab-earlier" });
    await harness.controller.load();

    expect(harness.elements.recoverySource?.textContent).toBe(`${EDITED}theirs\n`);
    window.dispatchEvent(new Event("pagehide"));

    harness.elements.recoveryDiscard?.click();

    expect(harness.recovery().hidden).toBe(false);
    expect(harness.elements.recoverySource?.textContent).toBe(`${EDITED}mine\n`);
  });
});

// The answers. Each button is the whole of what it does: one puts the text back
// and none of them sends anything anywhere.
describe("answering an offer of kept text", () => {
  // The reloaded tab's own record, put back. The hold on its slot ends with the
  // answer, so the text now on screen starts being kept again — restoring it and
  // then losing the tab a second time must not be how somebody loses it for good.
  it("starts keeping again once its own recovered text is back in the editor", async () => {
    const storage = memoryDraftStorage();
    seedResume(storage);
    const harness = mount({ storage, draftID: "tab-earlier" });
    await harness.controller.load();

    harness.elements.recoveryRestore?.click();

    expect(harness.surface().getValue()).toBe(EDITED);
    expect(harness.drafts()).toHaveLength(1);
    expect(harness.drafts()[0]).toMatchObject({ draftID: "tab-earlier", content: EDITED });

    harness.surface().type(`${EDITED}and more\n`);
    await tick();

    expect(harness.drafts()[0]).toMatchObject({ content: `${EDITED}and more\n` });
  });

  it("puts the text back in the editor on the button, and still writes nothing", async () => {
    document.title = "Edit Alpha - bn serve";
    const storage = memoryDraftStorage();
    seedResume(storage);
    const harness = mount({ storage });
    await harness.controller.load();
    const cleanTitle = document.title;

    harness.elements.recoveryRestore?.click();

    expect(harness.surface().getValue()).toBe(EDITED);
    expect(harness.controller.dirty()).toBe(true);
    expect(document.title).toBe(`• ${cleanTitle}`);
    expect(harness.recovery().hidden).toBe(true);
    expect(harness.draftStatus()).toContain("still not in the vault");
    expect(harness.client.save).not.toHaveBeenCalled();
    // The text moved into this tab's own slot; leaving it under the other tab's
    // id would offer it back a second time on the next load.
    expect(harness.drafts().map((draft) => draft.draftID)).toEqual(["tab-this"]);
  });

  // The save landed and the tab never learned it — a connection dropped right at
  // the end. Two identical texts is a question with no content.
  it("clears a draft the vault already holds and asks nothing", async () => {
    const storage = memoryDraftStorage();
    seedResume(storage, { content: LOADED });
    const harness = mount({ storage });
    await harness.controller.load();

    expect(harness.recovery().hidden).toBe(true);
    expect(harness.drafts()).toEqual([]);
  });

  it("takes a discarded draft off the page as well as out of storage", async () => {
    const storage = memoryDraftStorage();
    seedResume(storage);
    const harness = mount({ storage });
    await harness.controller.load();

    harness.elements.recoveryDiscard?.click();

    expect(harness.recovery().hidden).toBe(true);
    expect(harness.drafts()).toEqual([]);
    // Not merely hidden: a copy of somebody's discarded text left in the DOM is
    // still on the page, in a "save page as", and on the next person's screen.
    expect(harness.elements.recoverySource?.textContent).toBe("");
    expect(harness.draftStatus()).toContain("discarded");
  });

  // Two tabs are two questions, asked in turn.
  it("asks about the second tab's draft after the first is answered", async () => {
    const storage = memoryDraftStorage();
    seedResume(storage, { draftID: "tab-a", content: `${EDITED}a\n`, updatedAt: 9_000 });
    seedResume(storage, { draftID: "tab-b", content: `${EDITED}b\n`, updatedAt: 8_000 });
    const harness = mount({ storage });
    await harness.controller.load();

    expect(harness.elements.recoverySource?.textContent).toBe(`${EDITED}a\n`);
    harness.elements.recoveryDiscard?.click();

    expect(harness.recovery().hidden).toBe(false);
    expect(harness.elements.recoverySource?.textContent).toBe(`${EDITED}b\n`);
  });
});

// A standing offer was decided against the reading the load made, and this tab
// can make that reading out of date by itself.
describe("an offer left standing while this editor saves", () => {
  // The panel would otherwise go on saying "has not changed since, so putting it
  // back leaves you where you were" about a note this tab changed a minute ago —
  // and its Restore, still a resume, would arm an ordinary save of pre-save text
  // against the post-save revision and overwrite the save with no conflict panel
  // anywhere.
  it("re-asks its standing question against the version it just saved", async () => {
    const storage = memoryDraftStorage();
    seedResume(storage, { draftID: "tab-earlier" });
    const harness = mount({ storage, draftID: "tab-earlier" });
    await harness.controller.load();
    expect(harness.recoveryKind()).toBe("resume");

    harness.surface().type(`${EDITED}saved from here\n`);
    await harness.controller.save();

    expect(harness.recoveryKind()).toBe("stale");
    // And the record is still there: the save is this editor's text reaching the
    // vault and answers nothing about the older text on the panel.
    expect(harness.drafts()).toEqual([
      expect.objectContaining({ draftID: "tab-earlier", content: EDITED }),
    ]);
  });

  // The consequence, end to end: restoring what the panel now calls stale re-pins
  // the revision the draft was written against, so the save that follows is
  // refused and answered with the conflict panel rather than quietly replacing
  // the version this tab saved a moment ago.
  it("restores against the draft's own revision once the offer has gone stale", async () => {
    const storage = memoryDraftStorage();
    seedResume(storage, { draftID: "tab-earlier" });
    const harness = mount({
      storage,
      draftID: "tab-earlier",
      client: {
        save: vi
          .fn()
          .mockResolvedValueOnce(saveSuccess())
          .mockResolvedValue(saveFailure({ code: "conflict", revision: "r2", content: LOADED })),
      },
    });
    await harness.controller.load();
    harness.surface().type(`${EDITED}saved from here\n`);
    await harness.controller.save();

    harness.elements.recoveryRestore?.click();
    await harness.controller.save();

    expect(harness.client.save).toHaveBeenLastCalledWith(
      expect.objectContaining({ revision: "r1", content: EDITED })
    );
    expect(harness.elements.conflict?.hidden).toBe(false);
  });

  // The other way a standing question ends at a save: this editor writes exactly
  // the text the panel was offering, so the vault holds it and there is nothing
  // left to decide. Taking it down is right; stopping there is not, because the
  // record underneath in this tab's own slot was never part of that question.
  it("asks about this tab's own record once a save settles another tab's", async () => {
    const storage = memoryDraftStorage();
    seedResume(storage, { draftID: "tab-earlier", content: `${EDITED}mine\n`, updatedAt: 8_000 });
    seedResume(storage, { draftID: "tab-other", content: `${EDITED}theirs\n`, updatedAt: 9_000 });
    const harness = mount({ storage, draftID: "tab-earlier" });
    await harness.controller.load();
    expect(harness.elements.recoverySource?.textContent).toBe(`${EDITED}theirs\n`);

    harness.surface().type(`${EDITED}theirs\n`);
    await harness.controller.save();

    expect(harness.recovery().hidden).toBe(false);
    expect(harness.elements.recoverySource?.textContent).toBe(`${EDITED}mine\n`);
    expect(harness.drafts()).toEqual([
      expect.objectContaining({ draftID: "tab-earlier", content: `${EDITED}mine\n` }),
    ]);
  });
});
