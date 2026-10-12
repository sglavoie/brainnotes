// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import type { SourceClient } from "./source-save";
import {
  cleanup,
  type Harness,
  mount,
  saveFailure,
  saveSuccess,
  inConflict,
  savingOverAConflict,
} from "./source-editor-controller.test-helpers";

afterEach(cleanup);

describe("a note that changed under the edit", () => {
  it("shows the vault's version and leaves the draft in the editor", async () => {
    const harness = await inConflict();


    expect(harness.conflict().hidden).toBe(false);
    expect(harness.elements.conflictSource?.textContent).toBe("Somebody else's text.\n");
    expect(harness.surface().getValue()).toBe("my draft\n");
    expect(harness.controller.dirty()).toBe(true);
  });

  it("overwrites by re-pinning to the reported revision and resubmitting the same text", async () => {
    const harness = await inConflict();

    harness.elements.conflictOverwrite?.click();
    await vi.waitFor(() => expect(harness.client.save).toHaveBeenCalledTimes(2));

    expect(harness.client.save.mock.calls[1][0]).toMatchObject({ content: "my draft\n", revision: "r9" });
    expect(harness.conflict().hidden).toBe(true);
    expect(harness.controller.dirty()).toBe(false);
  });

  it("discards by adopting the vault's bytes and revision", async () => {
    const harness = await inConflict();

    harness.elements.conflictDiscard?.click();
    expect(harness.surface().getValue()).toBe("Somebody else's text.\n");
    expect(harness.controller.dirty()).toBe(false);
    expect(harness.conflict().hidden).toBe(true);

    harness.surface().type("a fresh edit\n");
    await harness.controller.save();
    expect(harness.client.save.mock.calls[1][0].revision).toBe("r9");
  });
});

describe("resolving a conflict", () => {
  // A 409 is the server saying it wrote nothing, so there is no unanswered
  // attempt left to resume and the overwrite is a new one. Carrying the refused
  // id forward would put a second, differently-pinned write under an identifier
  // whose whole purpose is naming one submission.
  it("overwrites under a new save id", async () => {
    const harness = await inConflict();

    harness.elements.conflictOverwrite?.click();
    await vi.waitFor(() => expect(harness.client.save).toHaveBeenCalledTimes(2));

    const [first, second] = harness.client.save.mock.calls.map((call) => call[0].saveID);
    expect(second).not.toBe(first);
    expect(second).not.toBe("");
  });

  // Discarding adopts the vault's bytes, so there is nothing unsaved left; a
  // Save button still armed here is an invitation to rewrite what was just
  // accepted.
  it("leaves nothing unsaved after a discard", async () => {
    const harness = await inConflict();
    expect(document.title).toBe("• Edit Alpha - bn serve");

    harness.elements.conflictDiscard?.click();
    expect(harness.saveButton().disabled).toBe(true);
    expect(harness.elements.conflict?.hasAttribute("data-source-conflict-revision")).toBe(false);
    expect(document.title).toBe("Edit Alpha - bn serve");
  });

});

// What the panel does while it is up, rather than which choice it applies.
//
// Two shapes. It comes down on its own when a later save *wrote* — that is what
// makes the version it holds obsolete rather than merely unchosen — and it
// declines both of its buttons while a save is in flight, because either one
// re-pins and tears the panel down over a request that has not answered. The
// last two are the adopted-text and successful-save cases of the same rule.
describe("the conflict panel's own lifetime", () => {
  // The panel is up for the whole of a later save, so either button can be
  // clicked over a request that has not answered yet. Each one re-pins the
  // revision and takes the panel down before calling save — which then refuses,
  // leaving the editor pinned to a version whose bytes nothing on screen still
  // holds and a choice nobody ever made.
  it("declines either choice while a save is in flight", async () => {
    const { harness, pending, inFlight } = await savingOverAConflict();

    harness.elements.conflictOverwrite?.click();
    harness.elements.conflictDiscard?.click();

    expect(harness.client.save).toHaveBeenCalledTimes(2);
    expect(harness.conflict().hidden).toBe(false);
    expect(harness.conflict().getAttribute("data-source-conflict-revision")).toBe("r9");
    expect(harness.surface().getValue()).toBe("my draft, revised\n");

    // The in-flight save is what decides the note's fate, and it succeeded, so
    // the panel goes — on its own terms rather than on a click that was
    // declined.
    pending.resolve(saveSuccess({ revision: "r11" }));
    await inFlight;
    expect(harness.conflict().hidden).toBe(true);
  });

  // A save that landed is a write: the note now holds the draft, so the version
  // on the panel is older than what is on disk. Leaving it up offers to re-pin
  // to a superseded revision, or to load bytes this save already replaced.
  // A refusal that wrote nothing is deliberately not treated this way — the
  // other writer's version is still the newest thing anybody has seen.
  it("takes the panel down when a later save lands without a snapshot", async () => {
    const harness = mount({
      save: vi
        .fn()
        .mockResolvedValueOnce(
          saveFailure({
            ok: false,
            code: "conflict",
            message: "source: Alpha.md changed since it was loaded, so nothing was written",
            revision: "r9",
            content: "Somebody else's text.\n",
          })
        )
        .mockResolvedValue(
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
    harness.surface().type("my draft\n");
    await harness.controller.save();
    expect(harness.conflict().hidden).toBe(false);

    harness.surface().type("my draft, revised\n");
    await harness.controller.save();
    expect(harness.conflict().hidden).toBe(true);
    expect(harness.conflict().hasAttribute("data-source-conflict-revision")).toBe(false);
  });

  it("previews the adopted text rather than the discarded draft", async () => {
    const harness = await inConflict();

    harness.elements.conflictDiscard?.click();
    const rendered = harness.client.preview.mock.calls.at(-1)?.[0];
    expect(rendered).toBe("Somebody else's text.\n");
  });

  // The panel holds one specific version of the note. Once a save succeeds that
  // version is history, and leaving it on screen would offer to overwrite the
  // note with bytes older than the ones just written.
  it("takes the panel down once a later save succeeds", async () => {
    const harness = await inConflict();
    expect(harness.conflict().hidden).toBe(false);

    harness.surface().type("my draft, revised\n");
    await harness.controller.save();
    expect(harness.conflict().hidden).toBe(true);
  });
});

// A precondition that failed somewhere other than the note.
// classifySourceSaveTxnError turns every transaction conflict into a revision
// error and the handler re-reads the note for all of them, so a configuration
// pin that moved at the commit boundary arrives looking exactly like a stale
// revision — code `conflict`, with a revision and content attached. What it
// carries is the note nobody touched, which is to say the editor's own base.
describe("a conflict over a version the editor already has", () => {
  // The message is the server's, verbatim. newSourceRevisionError writes
  // "changed since it was loaded" for every precondition that failed at the
  // commit boundary, so this is the sentence a config-pin refusal arrives with
  // — and it is false, which is why the editor substitutes its own below.
  const pinMoved = (): Harness =>
    mount({
      save: vi.fn().mockResolvedValue(
        saveFailure({
          ok: false,
          code: "conflict",
          message: "source: Alpha.md changed since it was loaded, so nothing was written",
          revision: "r1",
          content: "Alpha body.\n",
        })
      ) as unknown as SourceClient["save"],
    });

  // Showing the panel would ask somebody to choose between their draft and the
  // text they started from, and "discard" would then silently throw the draft
  // away in exchange for nothing.
  it("reports it without offering a merge", async () => {
    const harness = pinMoved();
    await harness.controller.load();
    harness.surface().type("my draft\n");

    await harness.controller.save();
    expect(harness.conflict().hidden).toBe(true);
    expect(harness.elements.status?.classList.contains("is-error")).toBe(true);
    expect(harness.surface().getValue()).toContain("my draft\n");
    expect(harness.controller.dirty()).toBe(true);
  });

  // Passing the server's sentence through would tell somebody their note moved
  // while showing them no other version and no way to merge it.
  it("does not repeat the server's claim that the note changed", async () => {
    const harness = pinMoved();
    await harness.controller.load();
    harness.surface().type("my draft\n");

    await harness.controller.save();
    expect(harness.status()).not.toContain("changed since it was loaded");
    expect(harness.status()).toContain("nothing was written");
    expect(harness.status()).toContain("press Save to try again");
  });

  // The server answered — a 409 is it saying it wrote nothing — so there is no
  // attempt of unknown outcome left to resume and pressing Save again is a new
  // one. Reusing the id here would instead meet the ledger's own refusal.
  it("treats the refusal as answered, so the retry is a fresh attempt", async () => {
    const harness = pinMoved();
    await harness.controller.load();
    harness.surface().type("my draft\n");

    await harness.controller.save();
    await harness.controller.save();
    const [first, second] = harness.client.save.mock.calls.map((call) => call[0].saveID);
    expect(second).not.toBe(first);
    // Fresh attempt, same pin. This is the only guard on it: the response
    // carries a `revision` field and the "landed" arm re-pins to exactly that,
    // so re-pinning here is an easy mistake to make and nothing else in this
    // file would catch it. The revision the editor validated is what protects
    // the retry.
    expect(harness.client.save.mock.calls[1][0].revision).toBe("r1");
  });
});

describe("an answer that was not this API's", () => {
  // A body that is not the save envelope — a proxy's own error page, a
  // truncated response — says nothing about whether the bytes landed. That is
  // exactly what the save_id ledger exists for, so the attempt has to survive.
  const unreadable = () =>
    mount({
      save: vi
        .fn()
        .mockResolvedValueOnce(saveFailure({ ok: false, code: "", message: "" }))
        .mockResolvedValue(saveSuccess()) as unknown as SourceClient["save"],
    });

  it("keeps the save id so the retry is a replay", async () => {
    const harness = unreadable();
    await harness.controller.load();
    harness.surface().type("edited\n");

    await harness.controller.save();
    expect(harness.status()).toContain("whether the note was written is unknown");
    expect(harness.controller.dirty()).toBe(true);

    await harness.controller.save();
    const [first, second] = harness.client.save.mock.calls.map((call) => call[0].saveID);
    expect(second).toBe(first);
  });

  // The id names one submission, not the button. The ledger records it against
  // a digest and refuses a second submission of different bytes under it, so
  // carrying it past further typing would answer the retry with the server's
  // own "retry with a fresh save id" — a sentence about an internal identifier
  // nobody in the browser can act on, over text they can see is unsaved.
  it("mints a new save id when the retry carries different text", async () => {
    const harness = unreadable();
    await harness.controller.load();
    harness.surface().type("edited\n");
    await harness.controller.save();

    harness.surface().type("and then some more\n");
    await harness.controller.save();

    const [first, second] = harness.client.save.mock.calls.map((call) => call[0].saveID);
    expect(second).not.toBe(first);
    expect(harness.client.save.mock.calls[1][0].content).toContain("and then some more\n");
  });

  // The mirror of it: a refusal this API *sent* means nothing was written, so
  // the next Save is a new attempt and must not be replayed away.
  it("mints a new save id after a refusal the server sent", async () => {
    const harness = mount({
      save: vi
        .fn()
        .mockResolvedValueOnce(
          saveFailure({ ok: false, code: "invalid_request", message: "source: frontmatter is not valid YAML" })
        )
        .mockResolvedValue(saveSuccess()) as unknown as SourceClient["save"],
    });
    await harness.controller.load();
    harness.surface().type("bad\n");

    await harness.controller.save();
    await harness.controller.save();
    const [first, second] = harness.client.save.mock.calls.map((call) => call[0].saveID);
    expect(second).not.toBe(first);
  });
});
