// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import { memoryDraftStorage } from "./source-draft.test-helpers";
import { type AttachmentClaims } from "./source-save";
import {
  cleanup,
  saveSuccess,
  saveFailure,
  mount,
  tick,
  seedResume,
  refusedSave,
  EDITED,
} from "./source-editor-drafts.test-helpers";

afterEach(cleanup);

describe("a note that goes missing while the tab is open", () => {
  it("offers to write the text back as a new note, and writes nothing by itself", async () => {
    const harness = mount({ client: refusedSave() });
    await harness.controller.load();
    harness.surface().type(EDITED);
    await harness.controller.save();

    expect(harness.status()).toContain("no longer in the vault");
    expect(harness.recoveryKind()).toBe("missing");
    expect(harness.elements.recoveryRecreate?.hidden).toBe(false);
    expect(harness.elements.recoverySource?.textContent).toBe(EDITED);
    expect(harness.client.create).not.toHaveBeenCalled();
  });


  it("creates the note at the path this page was opened for, on the button", async () => {
    const harness = mount({
      client: {
        ...refusedSave(),
        create: vi.fn().mockResolvedValue(saveSuccess({ created: true, message: "Created Alpha.md." })),
      },
    });
    await harness.controller.load();
    harness.surface().type(EDITED);
    await harness.controller.save();

    harness.elements.recoveryRecreate?.click();
    await tick();

    expect(harness.client.create).toHaveBeenCalledWith(
      expect.objectContaining({ relPath: "Alpha.md", content: EDITED })
    );
    expect(harness.status()).toContain("Created Alpha.md.");
    expect(harness.recovery().hidden).toBe(true);
    // Dropped before the navigation, never after: a copy left in storage would
    // be offered back later as unsaved text that is, by then, a file.
    expect(harness.drafts()).toEqual([]);
    expect(harness.navigate).toHaveBeenCalledWith("/n/alpha");

    // And it stays dropped through the page actually leaving. The editor still
    // shows text that differs from what it loaded — the create never moved this
    // page's revision — so an unload flush would otherwise write the copy back
    // out over the top of the drop, which is precisely what the browser suite
    // caught here and jsdom cannot reach with a mocked navigate.
    window.dispatchEvent(new Event("pagehide"));
    harness.controller.destroy();

    expect(harness.drafts()).toEqual([]);
  });
});

// The recreate is a write, so it owes the staged uploads this page is
// holding exactly as a save does. These two are that handover.
describe("the staged uploads a recreate carries", () => {
  // A recovered draft is text with links in it, and the files those links name
  // are still in this page's staging area. The recreate is the write that puts
  // both in the vault, so it owes the ids exactly as the save that could not
  // find its note did — and stops owing them only on what came back.
  it("carries this page's staged uploads into the recreate and settles what landed", async () => {
    const settled: string[][] = [];
    const claims: AttachmentClaims = {
      owner: "tab-this",
      pending: () => ["u-1", "u-2"],
      committed: (saved) => {
        settled.push(saved.map((item) => item.rel_path));
      },
    };
    const harness = mount({
      claims,
      client: {
        ...refusedSave(),
        create: vi.fn().mockResolvedValue(
          saveSuccess({
            created: true,
            attachments: [
              { upload_id: "u-1", rel_path: "attachments/one.png" },
              { upload_id: "u-2", rel_path: "attachments/two.png" },
            ],
          })
        ),
      },
    });
    await harness.controller.load();
    harness.surface().type(EDITED);
    // The save that went missing settled its own empty envelope on the way past;
    // what this test is about starts after it.
    await harness.controller.save();
    settled.length = 0;

    harness.elements.recoveryRecreate?.click();
    await tick();

    expect(harness.client.create).toHaveBeenCalledWith(
      expect.objectContaining({ owner: "tab-this", attachments: ["u-1", "u-2"] })
    );
    expect(settled).toEqual([["attachments/one.png", "attachments/two.png"]]);
  });

  // The note was not created, so nothing was committed — but the envelope is
  // still what decides, and an empty one leaves the ids with this page to send
  // again with the next attempt.
  it("settles only the empty envelope when the recreate is refused", async () => {
    const settled: string[][] = [];
    const claims: AttachmentClaims = {
      owner: "tab-this",
      pending: () => ["u-1"],
      committed: (saved) => {
        settled.push(saved.map((item) => item.rel_path));
      },
    };
    const harness = mount({
      claims,
      client: {
        ...refusedSave(),
        create: vi
          .fn()
          .mockResolvedValue(saveFailure({ code: "exists", message: "That path is taken." })),
      },
    });
    await harness.controller.load();
    harness.surface().type(EDITED);
    await harness.controller.save();
    settled.length = 0;

    harness.elements.recoveryRecreate?.click();
    await tick();

    expect(settled).toEqual([[]]);
    expect(harness.status()).toContain("That path is taken.");
    expect(harness.recovery().hidden).toBe(false);
  });
});

describe("a note that goes missing, after the offer is up", () => {
  // The refusal is a fresh reading of the vault, and it has to replace the one
  // the load made. Otherwise the next question asked — another tab's draft for
  // the same note — is asked against a note that was there ten minutes ago:
  // "has not changed since, so putting it back leaves you where you were", with
  // a Restore button whose save can only be refused again.
  it("asks about the next tab's draft against the vault as it is now", async () => {
    const storage = memoryDraftStorage();
    seedResume(storage, { draftID: "tab-other", content: `${EDITED}elsewhere\n` });
    const harness = mount({ storage, client: refusedSave() });
    await harness.controller.load();
    expect(harness.recoveryKind()).toBe("resume");

    harness.surface().type(EDITED);
    await harness.controller.save();
    expect(harness.recoveryKind()).toBe("missing");

    harness.elements.recoveryDiscard?.click();

    expect(harness.recovery().hidden).toBe(false);
    expect(harness.recoveryKind()).toBe("missing");
    expect(harness.elements.recoverySource?.textContent).toBe(`${EDITED}elsewhere\n`);
    expect(harness.elements.recoveryRestore?.hidden).toBe(true);
  });

  // "No thank you" answers the offer, not the keeping. The text on this panel is
  // still in the editor and still unsaved, so taking the copy away would leave
  // the person one interruption from losing what they are looking at — while the
  // line told them it had been discarded.
  it("goes on keeping the text when its own recreate offer is discarded", async () => {
    const harness = mount({ client: refusedSave() });
    await harness.controller.load();
    harness.surface().type(EDITED);
    await harness.controller.save();
    await tick();
    expect(harness.recoveryKind()).toBe("missing");
    expect(harness.drafts()).toHaveLength(1);

    harness.elements.recoveryDiscard?.click();

    // Asserted before the debounce runs, which is the point: a discard that
    // dropped the record and let the next write put it back would still leave a
    // window in which the text on screen is kept nowhere at all.
    expect(harness.recovery().hidden).toBe(true);
    expect(harness.surface().getValue()).toBe(EDITED);
    expect(harness.drafts()).toHaveLength(1);
    expect(harness.drafts()[0]).toMatchObject({ draftID: "tab-this", content: EDITED });
  });

});

// The other half of a note that has gone: the button that writes the text back
// as a new note, which is the one action on this panel that leaves the page.
describe("recreating a note that has gone", () => {
  // The offer on screen is the editor's own text, but the synthetic draft behind
  // it carries this tab's id — and the slot is still holding a previous session's
  // record nobody has answered for. The recreated text is a file by the time this
  // runs; that record is still the only copy of what it holds.
  it("leaves an unanswered record alone when it recreates from the editor", async () => {
    const storage = memoryDraftStorage();
    seedResume(storage, { draftID: "tab-earlier" });
    const harness = mount({
      storage,
      draftID: "tab-earlier",
      client: { ...refusedSave(), create: vi.fn().mockResolvedValue(saveSuccess({ created: true })) },
    });
    await harness.controller.load();
    harness.surface().type(`${EDITED}newer\n`);
    await harness.controller.save();
    expect(harness.recoveryKind()).toBe("missing");

    harness.elements.recoveryRecreate?.click();
    await tick();

    expect(harness.client.create).toHaveBeenCalledWith(
      expect.objectContaining({ content: `${EDITED}newer\n` })
    );
    expect(harness.drafts()).toEqual([
      expect.objectContaining({ draftID: "tab-earlier", content: EDITED }),
    ]);
  });

  // The buttons stay on screen while the request is in flight, and that request
  // is what decides whether this page still exists. A second press would write a
  // second note; a discard landing in the gap would answer an offer whose
  // callback is about to act on the answer it no longer has.
  it("ignores a second press and a discard while the create is in flight", async () => {
    const harness = mount({
      client: {
        ...refusedSave(),
        create: vi.fn().mockResolvedValue(saveSuccess({ created: true })),
      },
    });
    await harness.controller.load();
    harness.surface().type(EDITED);
    await harness.controller.save();

    harness.elements.recoveryRecreate?.click();
    harness.elements.recoveryRecreate?.click();
    harness.elements.recoveryDiscard?.click();
    await tick();

    expect(harness.client.create).toHaveBeenCalledTimes(1);
    expect(harness.navigate).toHaveBeenCalledTimes(1);
    expect(harness.recovery().hidden).toBe(true);
    expect(harness.elements.status?.classList.contains("is-error")).toBe(false);
  });

  it("keeps the text and the offer when the create is refused", async () => {
    const harness = mount({
      client: {
        ...refusedSave(),
        create: vi
          .fn()
          .mockResolvedValue(saveFailure({ code: "conflict", message: "source: Alpha.md already exists" })),
      },
    });
    await harness.controller.load();
    harness.surface().type(EDITED);
    await harness.controller.save();

    harness.elements.recoveryRecreate?.click();
    await tick();

    expect(harness.status()).toContain("already exists");
    expect(harness.recovery().hidden).toBe(false);
    expect(harness.surface().getValue()).toBe(EDITED);
    expect(harness.navigate).not.toHaveBeenCalled();
  });
});
