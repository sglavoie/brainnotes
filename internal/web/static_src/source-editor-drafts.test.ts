// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import type { SourceSaveResponse } from "./api-types-source";
import { createSourceDraftStore } from "./source-draft";
import {
  draftKey,
  memoryDraftStorage,
  readDrafts,
  savedKey,
  seedDraft,
  type FakeDraftStorage,
} from "./source-draft.test-helpers";
import { createSourceEditorController } from "./source-editor-controller";
import {
  readSourceEditorConfig,
  readSourceEditorElements,
  type SourceEditorElements,
} from "./source-editor-elements";
import { createFakeSurfaceFactory, editorMarkup, type FakeSurface } from "./source-editor.test-helpers";
import {
  SourceReadError,
  notFoundCode,
  type AttachmentClaims,
  type SourceClient,
} from "./source-save";

// The draft machinery through the editor that owns it.
//
// These are deliberately controller-level rather than unit tests of the keeper.
// The session's obligations are about what happens to somebody's words across a
// reload, a refusal and a lost server, and each of them is a statement about the
// editor, the store and the transport together. The rule underneath all of them
// — nothing here ever submits a recovered draft by itself — is asserted case by
// case, as `save` and `create` never having been called.

const VAULT = "sync:vault-under-test";
const KEY = "alpha";
const LOADED = "---\ntitle: Alpha\n---\n\nAlpha body.\n";
const EDITED = "---\ntitle: Alpha\n---\n\nAlpha body, edited.\n";

const saveSuccess = (payload: Partial<SourceSaveResponse> = {}): SourceSaveResponse =>
  ({
    ok: true,
    key: KEY,
    path: "/n/alpha",
    rel_path: "Alpha.md",
    revision: "r2",
    message: "Saved Alpha.md.",
    changed: true,
    created: false,
    replayed: false,
    ...payload,
  }) as SourceSaveResponse;

const saveFailure = (payload: {
  code: string;
  message?: string;
  revision?: string;
  content?: string;
}): SourceSaveResponse => ({
  ok: false,
  message: "",
  revision: "",
  content: "",
  changed: false,
  attachments: [],
  ...payload,
});

type ClientMocks = {
  read: ReturnType<typeof vi.fn>;
  preview: ReturnType<typeof vi.fn>;
  save: ReturnType<typeof vi.fn>;
  create: ReturnType<typeof vi.fn>;
  search: ReturnType<typeof vi.fn>;
};

type Harness = {
  elements: SourceEditorElements;
  controller: ReturnType<typeof createSourceEditorController>;
  storage: FakeDraftStorage;
  surface(): FakeSurface;
  client: ClientMocks;
  navigate: ReturnType<typeof vi.fn>;
  status(): string;
  draftStatus(): string;
  recovery(): HTMLElement;
  recoveryKind(): string;
  drafts(): ReturnType<typeof readDrafts>;
};

type MountOptions = {
  client?: Partial<Record<keyof SourceClient, unknown>>;
  storage?: FakeDraftStorage | null;
  draftID?: string;
  claims?: AttachmentClaims | null;
};

// Every controller mounted here is torn down afterwards. Two of its listeners
// are on the document and the window rather than on the markup, so clearing the
// body alone would leave an earlier test's editor still answering `storage` and
// `beforeunload` for the rest of the file — and that window is the very channel
// two of these tests use.
const mounted: { destroy(): void }[] = [];

const mount = (options: MountOptions = {}): Harness => {
  document.body.innerHTML = editorMarkup();
  const panel = document.querySelector<HTMLElement>("[data-source-editor]");
  if (!panel) {
    throw new Error("fixture is missing the editor panel");
  }
  const config = readSourceEditorConfig(panel);
  const elements = readSourceEditorElements(panel);
  if (!config || !elements) {
    throw new Error("fixture does not describe a usable editor");
  }
  expect(config.vaultID).toBe(VAULT);

  const client = {
    read: vi.fn().mockResolvedValue({
      key: KEY,
      path: "/n/alpha",
      rel_path: "Alpha.md",
      revision: "r1",
      content: LOADED,
    }),
    preview: vi.fn().mockResolvedValue("<p>Alpha body.</p>"),
    save: vi.fn().mockResolvedValue(saveSuccess()),
    create: vi.fn().mockResolvedValue(saveSuccess({ created: true })),
    search: vi.fn().mockResolvedValue([]),
    ...options.client,
  } as unknown as ClientMocks;

  const storage = options.storage === undefined ? memoryDraftStorage() : options.storage;
  const now = () => 10_000;
  const surfaces = createFakeSurfaceFactory();
  const navigate = vi.fn();
  const controller = createSourceEditorController({
    elements,
    config,
    client: client as unknown as SourceClient,
    createSurface: surfaces.factory,
    previewDelayMs: 0,
    drafts: createSourceDraftStore({
      vaultID: config.vaultID,
      noteKey: config.key,
      draftID: options.draftID ?? "tab-this",
      storage,
      now,
    }),
    draftDelayMs: 0,
    now,
    navigate,
    claims: options.claims ?? null,
  });
  mounted.push(controller);

  return {
    elements,
    controller,
    client,
    navigate,
    storage: storage as FakeDraftStorage,
    surface: () => {
      const surface = surfaces.surface();
      if (!surface) {
        throw new Error("the surface was never mounted");
      }
      return surface;
    },
    status: () => elements.status?.textContent ?? "",
    draftStatus: () => elements.draftStatus?.textContent ?? "",
    recovery: () => {
      if (!elements.recovery) {
        throw new Error("fixture is missing the recovery panel");
      }
      return elements.recovery;
    },
    recoveryKind: () => elements.recovery?.getAttribute("data-source-recovery-kind") ?? "",
    drafts: () => readDrafts(storage as FakeDraftStorage),
  };
};

// The keep is debounced, so a test that types has to let the timer run. Zero is
// still a macrotask.
const tick = (): Promise<void> => new Promise((resolve) => setTimeout(resolve, 0));

afterEach(() => {
  while (mounted.length > 0) {
    mounted.pop()?.destroy();
  }
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

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

const seedResume = (storage: FakeDraftStorage, overrides: Record<string, unknown> = {}) =>
  seedDraft(storage, {
    vaultID: VAULT,
    noteKey: KEY,
    draftID: "tab-earlier",
    relPath: "Alpha.md",
    baseRevision: "r1",
    content: EDITED,
    updatedAt: 9_000,
    ...overrides,
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
    const storage = memoryDraftStorage();
    seedResume(storage);
    const harness = mount({ storage });
    await harness.controller.load();

    harness.elements.recoveryRestore?.click();

    expect(harness.surface().getValue()).toBe(EDITED);
    expect(harness.controller.dirty()).toBe(true);
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

// The save that comes back "there is no such note": a rename, a move, or a
// deletion that happened somewhere else while this tab was open.
const refusedSave = () => ({
  save: vi
    .fn()
    .mockResolvedValue(
      saveFailure({ code: notFoundCode, message: "source: Alpha.md is no longer in the vault" })
    ),
});

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

describe("a browser that will not keep a copy", () => {
  // Said before a single keystroke, not after the first failed write: somebody
  // typing into a browser with no safety net should learn that while the note is
  // still short enough to retype.
  it("warns before anything is typed and leaves the editor entirely usable", async () => {
    const harness = mount({ storage: null });

    expect(harness.draftStatus()).toContain("will not keep a local copy");
    await harness.controller.load();
    harness.surface().type(EDITED);
    await tick();
    await harness.controller.save();

    expect(harness.status()).toContain("Saved Alpha.md.");
    expect(harness.client.save).toHaveBeenCalledTimes(1);
    // And it is still said afterwards. Nothing in a load or a save makes this
    // browser start keeping copies, so the line that says so may not be cleared
    // by either — there is no later write to bring it back.
    expect(harness.draftStatus()).toContain("will not keep a local copy");
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
    expect(harness.draftStatus()).toContain("kept in this browser only");
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
    expect(harness.draftStatus()).toContain("kept in this browser only");
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
