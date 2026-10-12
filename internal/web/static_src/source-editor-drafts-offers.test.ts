// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import { memoryDraftStorage, savedKey } from "./source-draft.test-helpers";
import { SourceReadError, notFoundCode } from "./source-save";
import {
  cleanup,
  VAULT,
  saveFailure,
  mount,
  tick,
  seedResume,
  KEY,
  LOADED,
  EDITED,
} from "./source-editor-drafts.test-helpers";

afterEach(cleanup);

// The answer ends the recovery, whichever record it was about. Both of these
// are about the slot afterwards: one tab, one slot, and the thing in it has to
// be whatever the person is actually looking at.
describe("the slot after an offer is answered", () => {
  // Restoring another tab's draft is still an answer to this tab's question:
  // the text is now what this editor holds and what an interruption would take.
  // A hold that outlived the question it was raised for would leave that text
  // kept nowhere at all for the rest of the session.
  it("keeps what it restored, even when the record restored was another tab's", async () => {
    const storage = memoryDraftStorage();
    seedResume(storage, { draftID: "tab-earlier", content: `${EDITED}mine\n`, updatedAt: 8_000 });
    seedResume(storage, { draftID: "tab-other", content: `${EDITED}theirs\n`, updatedAt: 9_000 });
    const harness = mount({ storage, draftID: "tab-earlier" });
    await harness.controller.load();
    expect(harness.elements.recoverySource?.textContent).toBe(`${EDITED}theirs\n`);

    harness.elements.recoveryRestore?.click();

    expect(harness.surface().getValue()).toBe(`${EDITED}theirs\n`);
    expect(harness.drafts()).toEqual([
      expect.objectContaining({ draftID: "tab-earlier", content: `${EDITED}theirs\n` }),
    ]);
    // And the line says what that cost. The record this tab had in the same slot
    // — a previous session's, never answered for — is what the restore wrote
    // over, and there is no second slot it could have gone to. Somebody who is
    // told their text is back should not also be losing another copy of it in
    // silence.
    expect(harness.draftStatus()).toContain("from an earlier session is gone");

    harness.surface().type(`${EDITED}theirs, and more\n`);
    await tick();

    expect(harness.drafts()[0]).toMatchObject({ content: `${EDITED}theirs, and more\n` });
  });

  // Discarding re-asks from what is left in storage, and by then this tab's own
  // slot holds the live text the writer put there. Asking about that would offer
  // somebody back the words already in front of them, with a Discard button that
  // throws away their only copy of them.
  it("does not offer back the text the editor is holding right now", async () => {
    const storage = memoryDraftStorage();
    seedResume(storage, { draftID: "tab-earlier", content: `${EDITED}mine\n`, updatedAt: 9_000 });
    seedResume(storage, { draftID: "tab-other", content: `${EDITED}theirs\n`, updatedAt: 8_000 });
    const harness = mount({ storage, draftID: "tab-earlier" });
    await harness.controller.load();
    harness.surface().type(`${EDITED}live\n`);

    harness.elements.recoveryDiscard?.click();
    expect(harness.elements.recoverySource?.textContent).toBe(`${EDITED}theirs\n`);
    await tick();
    expect(harness.drafts().map((draft) => draft.draftID).sort()).toEqual([
      "tab-earlier",
      "tab-other",
    ]);

    harness.elements.recoveryDiscard?.click();

    expect(harness.recovery().hidden).toBe(true);
  });

  // An answer about another tab's record is not an answer about this tab's own,
  // which is still standing on the slot — so what is being typed still goes
  // nowhere. The line has to say so again, because this very discard replaced the
  // sentence that said it the first time, and a person left reading "discarded,
  // the editor is unchanged" over a tab keeping nothing is exactly who this
  // feature exists for.
  it("says again that nothing is being kept when the discard was about another tab", async () => {
    const storage = memoryDraftStorage();
    seedResume(storage, { draftID: "tab-earlier", content: `${EDITED}mine\n`, updatedAt: 8_000 });
    seedResume(storage, { draftID: "tab-other", content: `${EDITED}theirs\n`, updatedAt: 9_000 });
    const harness = mount({ storage, draftID: "tab-earlier" });
    await harness.controller.load();

    harness.surface().type(`${EDITED}live\n`);
    await tick();
    expect(harness.draftStatus()).toContain("still being kept in this browser");

    harness.elements.recoveryDiscard?.click();
    await tick();

    expect(harness.elements.recoverySource?.textContent).toBe(`${EDITED}mine\n`);
    expect(harness.draftStatus()).toContain("what you are typing now is not");
    expect(harness.drafts()).toEqual([
      expect.objectContaining({ draftID: "tab-earlier", content: `${EDITED}mine\n` }),
    ]);
  });
});

// The same offer, made when the load could not say "the note is exactly as you
// left it" — because it moved, because the note is gone, or because nothing
// answered at all. Each one is a different set of buttons.
describe("offering kept text back over a vault that moved or did not answer", () => {
  // The note moved on while the tab was away. Restoring is still the right
  // answer, but it has to put the two versions side by side rather than arm a
  // save against a revision the vault has replaced.
  it("shows both versions when the note moved on, and writes neither", async () => {
    const storage = memoryDraftStorage();
    seedResume(storage, { baseRevision: "r0" });
    const harness = mount({ storage });
    await harness.controller.load();

    expect(harness.recoveryKind()).toBe("stale");
    harness.elements.recoveryRestore?.click();

    expect(harness.surface().getValue()).toBe(EDITED);
    expect(harness.elements.conflict?.hidden).toBe(false);
    expect(harness.elements.conflictSource?.textContent).toBe(LOADED);
    expect(harness.elements.conflict?.getAttribute("data-source-conflict-revision")).toBe("r1");
    expect(harness.client.save).not.toHaveBeenCalled();
  });

  // And the save that follows is planned against the revision the draft was
  // written against — which is simply true, and is what puts the server's own
  // refusal in front of the decision instead of letting a recovered draft
  // quietly overwrite whatever landed since.
  it("plans a save after a stale restore against the draft's own revision", async () => {
    const storage = memoryDraftStorage();
    seedResume(storage, { baseRevision: "r0" });
    const harness = mount({
      storage,
      client: {
        save: vi.fn().mockResolvedValue(saveFailure({ code: "conflict", revision: "r1", content: LOADED })),
      },
    });
    await harness.controller.load();
    harness.elements.recoveryRestore?.click();

    await harness.controller.save();

    expect(harness.client.save).toHaveBeenCalledWith(
      expect.objectContaining({ revision: "r0", content: EDITED })
    );
    expect(harness.elements.conflict?.hidden).toBe(false);
  });

  // Network loss, a stopped server, an expired Access session. Nothing could be
  // compared, so nothing is claimed and — the load-bearing half — nothing is
  // dropped.
  it("keeps and shows the draft when the vault could not be reached", async () => {
    const storage = memoryDraftStorage();
    seedResume(storage);
    const harness = mount({
      storage,
      client: { read: vi.fn().mockRejectedValue(new Error("network error")) },
    });
    await harness.controller.load();

    expect(harness.recoveryKind()).toBe("unreachable");
    expect(harness.elements.recoveryRestore?.hidden).toBe(true);
    expect(harness.elements.recoveryRecreate?.hidden).toBe(true);
    expect(harness.drafts()).toHaveLength(1);
    expect(harness.status()).toContain("network error");

    // Through the page leaving, which is the assertion that matters: the panel
    // has just promised this text will still be here when the note can be
    // loaded again, and the only enabled button on it is Discard. A load that
    // produced no editor is not an editor with nothing unsaved — nothing was
    // compared, so nothing may be concluded and nothing may be dropped.
    window.dispatchEvent(new Event("pagehide"));
    harness.controller.destroy();

    expect(harness.drafts()).toHaveLength(1);
  });

  // The other failure that arrives at the same catch, and means the opposite:
  // the vault answering that the note is not there.
  it("offers a new note when the target is gone at load time", async () => {
    const storage = memoryDraftStorage();
    seedResume(storage);
    const harness = mount({
      storage,
      client: {
        read: vi.fn().mockRejectedValue(new SourceReadError("source: no note for key", notFoundCode)),
      },
    });
    await harness.controller.load();

    expect(harness.recoveryKind()).toBe("missing");
    expect(harness.elements.recoveryRecreate?.hidden).toBe(false);
    expect(harness.elements.recoveryRestore?.hidden).toBe(true);
    expect(harness.client.create).not.toHaveBeenCalled();
  });
});

describe("another tab on the same note", () => {
  const announce = (revision: string): void => {
    window.dispatchEvent(
      new StorageEvent("storage", {
        key: savedKey(VAULT, KEY),
        newValue: JSON.stringify({ revision, at: 10_000 }),
      })
    );
  };

  // A notice, and only ever a notice. This tab has its own unsaved text and its
  // own pinned revision, and adopting somebody else's save here would overwrite
  // the first with a version of the second nobody asked for.
  it("reports a peer's save without adopting anything", async () => {
    const harness = mount();
    await harness.controller.load();
    harness.surface().type(EDITED);
    await tick();

    announce("r9");

    expect(harness.draftStatus()).toContain("Another tab saved this note");
    expect(harness.surface().getValue()).toBe(EDITED);
    expect(harness.drafts()).toHaveLength(1);
    expect(harness.client.read).toHaveBeenCalledTimes(1);
  });

  // The peer's news is the newest true thing, so it takes the line. It must not
  // take it for good: this is the one line that says the text on screen has
  // never left the browser, and leaving somebody's session with a sentence
  // containing the word "saved" in its place is the mislabeling this whole
  // feature is written against.
  it("gives the line back to what it is keeping as soon as there is more to keep", async () => {
    const harness = mount();
    await harness.controller.load();
    harness.surface().type(EDITED);
    await tick();

    announce("r9");
    expect(harness.draftStatus()).toMatch(/\bsaved\b/i);

    harness.surface().type(`${EDITED}more\n`);
    await tick();

    expect(harness.draftStatus()).toContain("kept in this browser only");
    expect(harness.draftStatus()).not.toMatch(/\bsaved\b/i);
  });

  // This tab's own save comes back through the same channel in every other tab,
  // including one already holding that revision. Announcing a revision somebody
  // already has is noise on the line that has to be believed when it says
  // something changed.
  it("says nothing about a revision this tab already holds", async () => {
    const harness = mount();
    await harness.controller.load();
    harness.surface().type(EDITED);
    await tick();
    const before = harness.draftStatus();

    announce("r1");

    expect(harness.draftStatus()).toBe(before);
  });

  it("tells the other tabs when it saves", async () => {
    const harness = mount();
    await harness.controller.load();
    harness.surface().type(EDITED);
    await harness.controller.save();

    const announced = harness.storage.getItem(savedKey(VAULT, KEY));
    expect(announced).not.toBeNull();
    expect(JSON.parse(announced ?? "null")).toMatchObject({ revision: "r2" });
  });
});
