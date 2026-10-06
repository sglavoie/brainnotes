// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import { createSourceEditorController } from "./source-editor-controller";
import {
  readSourceEditorConfig,
  readSourceEditorElements,
  type SourceEditorConfig,
  type SourceEditorElements,
} from "./source-editor-elements";
import { createFakeSurfaceFactory, editorMarkup, type FakeSurface } from "./source-editor.test-helpers";
import type { AttachmentClaims, SourceClient } from "./source-save";
import type { SourceSaveResponse } from "./api";

const LOADED = "---\ntitle: Alpha\n---\n\nAlpha body.\n";

type Harness = {
  elements: SourceEditorElements;
  config: SourceEditorConfig;
  controller: ReturnType<typeof createSourceEditorController>;
  surface(): FakeSurface;
  client: {
    read: ReturnType<typeof vi.fn>;
    preview: ReturnType<typeof vi.fn>;
    save: ReturnType<typeof vi.fn>;
    search: ReturnType<typeof vi.fn>;
  };
  status(): string;
  saveButton(): HTMLButtonElement;
  conflict(): HTMLElement;
};

// Every controller mounted here is torn down afterwards, and that is not
// tidiness. Two of its listeners are on the document and the window rather than
// on the markup, so clearing `document.body` leaves them bound to a detached
// fixture: a dirty editor from an earlier test goes on answering `beforeunload`
// for the rest of the file.
const mounted: { destroy(): void }[] = [];

const mount = (
  overrides: Partial<SourceClient> = {},
  previewDelayMs = 0,
  claims: AttachmentClaims | null = null
): Harness => {
  document.body.innerHTML = editorMarkup();
  document.title = "Edit Alpha - bn serve";
  const panel = document.querySelector<HTMLElement>("[data-source-editor]");
  if (!panel) {
    throw new Error("fixture is missing the editor panel");
  }
  const config = readSourceEditorConfig(panel);
  const elements = readSourceEditorElements(panel);
  if (!config || !elements) {
    throw new Error("fixture does not describe a usable editor");
  }

  const client = {
    read: vi.fn().mockResolvedValue({
      key: "alpha",
      path: "/n/alpha",
      rel_path: "Alpha.md",
      revision: "r1",
      content: LOADED,
    }),
    preview: vi.fn().mockResolvedValue("<p>Alpha body.</p>"),
    save: vi.fn().mockResolvedValue({ ok: true, revision: "r2", message: "Saved Alpha.md." }),
    search: vi.fn().mockResolvedValue([]),
    ...overrides,
  };

  const surfaces = createFakeSurfaceFactory();
  const controller = createSourceEditorController({
    elements,
    config,
    client: client as unknown as SourceClient,
    createSurface: surfaces.factory,
    previewDelayMs,
    claims,
  });
  mounted.push(controller);

  return {
    elements,
    config,
    controller,
    client: client as Harness["client"],
    surface: () => {
      const surface = surfaces.surface();
      if (!surface) {
        throw new Error("the surface has not been mounted yet");
      }
      return surface;
    },
    status: () => elements.status?.textContent ?? "",
    saveButton: () => {
      if (!elements.save) {
        throw new Error("fixture is missing the save button");
      }
      return elements.save;
    },
    conflict: () => {
      if (!elements.conflict) {
        throw new Error("fixture is missing the conflict panel");
      }
      return elements.conflict;
    },
  };
};

const saveFailure = (payload: Partial<SourceSaveResponse> & { ok: false }): SourceSaveResponse =>
  ({
    code: "",
    message: "",
    revision: "",
    content: "",
    changed: false,
    attachments: [],
    ...payload,
  }) as SourceSaveResponse;

const saveSuccess = (payload: Partial<SourceSaveResponse> = {}): SourceSaveResponse =>
  ({
    ok: true,
    key: "",
    path: "",
    rel_path: "",
    revision: "r2",
    message: "Saved.",
    changed: true,
    created: false,
    replayed: false,
    attachments: [],
    ...payload,
  }) as SourceSaveResponse;

afterEach(() => {
  while (mounted.length > 0) {
    mounted.pop()?.destroy();
  }
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

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

// conflicting refuses the first save with the stale-revision envelope and
// accepts everything after it, which is what every conflict below starts from.
const conflicting = (): Harness =>
  mount({
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
      .mockResolvedValue({ ok: true, revision: "r10", message: "Saved Alpha.md." }) as unknown as SourceClient["save"],
  });

// inConflict drives one to the point where the panel is up and the draft is
// still the editor's.
const inConflict = async (): Promise<Harness> => {
  const harness = conflicting();
  await harness.controller.load();
  harness.surface().type("my draft\n");
  await harness.controller.save();
  return harness;
};

const deferred = <T,>(): { promise: Promise<T>; resolve: (value: T) => void } => {
  let resolve: (value: T) => void = () => undefined;
  const promise = new Promise<T>((settle) => {
    resolve = settle;
  });
  return { promise, resolve };
};

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

// savingOverAConflict is the overlap the two conflict buttons have to survive:
// the panel is up, and a *later* save is in flight and has not answered. The
// second response is a promise this test resolves by hand, because nothing else
// can hold a request open across a click.
const savingOverAConflict = async (): Promise<{
  harness: Harness;
  pending: { resolve: (value: SourceSaveResponse) => void };
  inFlight: Promise<void>;
}> => {
  const pending = deferred<SourceSaveResponse>();
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
      .mockReturnValueOnce(pending.promise) as unknown as SourceClient["save"],
  });
  await harness.controller.load();
  harness.surface().type("my draft\n");
  await harness.controller.save();

  harness.surface().type("my draft, revised\n");
  const inFlight = harness.controller.save();
  return { harness, pending, inFlight };
};

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

// A staged upload is a file on the server that no note mentions. The save is
// what turns it into part of the vault, so these tests are about the two halves
// of that handover: the claim going out with the request, and the page stopping
// owing an id only on the strength of what came back.
type StubClaims = AttachmentClaims & { settled: readonly unknown[][] };

const stubClaims = (pending: string[]): StubClaims => {
  const settled: readonly unknown[][] = [];
  return {
    owner: "tab-7",
    pending: () => pending,
    committed: (saved) => {
      (settled as unknown[][]).push([...saved]);
    },
    settled,
  };
};

const committed = [{ upload_id: "u-1", rel_path: "attachments/shot.png" }];

describe("attachment claims on save", () => {
  it("sends the owner and the ids this page is holding", async () => {
    const claims = stubClaims(["u-1", "u-2"]);
    const harness = mount({}, 0, claims);
    await harness.controller.load();
    harness.surface().type("edited\n");

    await harness.controller.save();
    expect(harness.client.save.mock.calls[0][0]).toMatchObject({
      owner: "tab-7",
      attachments: ["u-1", "u-2"],
    });
  });

  it("settles what the vault answered, not what the request asked for", async () => {
    const claims = stubClaims(["u-1", "u-2"]);
    const harness = mount(
      {
        save: vi
          .fn()
          .mockResolvedValue(saveSuccess({ attachments: committed })) as unknown as SourceClient["save"],
      },
      0,
      claims
    );
    await harness.controller.load();
    harness.surface().type("edited\n");

    await harness.controller.save();
    expect(claims.settled).toEqual([committed]);
  });

});

// The three answers that are not a plain success. What a save committed is the
// server's statement, so each of these is about believing that statement rather
// than the request that provoked it.
describe("attachment claims a save did not plainly commit", () => {
  // The bytes are on disk; only the content history missed them. A page that
  // went on owing those ids would cancel them on the way out and delete
  // attachments the note already links.
  it("settles on the snapshot failure too, because that arm committed them as well", async () => {
    const claims = stubClaims(["u-1"]);
    const harness = mount(
      {
        save: vi.fn().mockResolvedValue(
          saveFailure({
            ok: false,
            code: "snapshot_failed",
            message: "the change is on disk and further writes are blocked",
            revision: "r7",
            changed: true,
            attachments: committed,
          })
        ) as unknown as SourceClient["save"],
      },
      0,
      claims
    );
    await harness.controller.load();
    harness.surface().type("edited\n");

    await harness.controller.save();
    expect(claims.settled).toEqual([committed]);
  });

  // A conflict wrote nothing, so the ids are still this page's to send with the
  // merge that follows.
  it("settles nothing a refusal did not commit", async () => {
    const claims = stubClaims(["u-1"]);
    const harness = mount(
      {
        save: vi.fn().mockResolvedValue(
          saveFailure({
            ok: false,
            code: "revision_conflict",
            message: "The note changed since it was read.",
            content: "theirs\n",
            revision: "r9",
          })
        ) as unknown as SourceClient["save"],
      },
      0,
      claims
    );
    await harness.controller.load();
    harness.surface().type("edited\n");

    await harness.controller.save();
    expect(claims.settled).toEqual([[]]);
  });

  // The request never produced an answer, so what the vault holds is unknown.
  // Settling here would drop an id whose file may never have been committed.
  it("settles nothing when the request never came back", async () => {
    const claims = stubClaims(["u-1"]);
    const harness = mount(
      { save: vi.fn().mockRejectedValue(new Error("offline")) as unknown as SourceClient["save"] },
      0,
      claims
    );
    await harness.controller.load();
    harness.surface().type("edited\n");

    await harness.controller.save();
    expect(claims.settled).toEqual([]);
  });
});

// The save shortcut. It is bound on the document rather than only in the
// editor's keymap, so it reaches Save from the toolbar and the conflict
// buttons too — and is therefore the one path into a save that the Save
// button's disabled state cannot speak for.
describe("the save shortcut", () => {
  it("saves on Ctrl/⌘+S from anywhere on the page and stops the browser's own handler", async () => {
    const harness = mount();
    await harness.controller.load();
    harness.surface().type("edited\n");

    const event = new KeyboardEvent("keydown", { key: "s", ctrlKey: true, cancelable: true, bubbles: true });
    document.body.dispatchEvent(event);
    expect(event.defaultPrevented).toBe(true);
    await vi.waitFor(() => expect(harness.client.save).toHaveBeenCalledTimes(1));
  });

  it("leaves an unmodified S alone", async () => {
    const harness = mount();
    await harness.controller.load();
    harness.surface().type("edited\n");

    document.body.dispatchEvent(new KeyboardEvent("keydown", { key: "s", cancelable: true, bubbles: true }));
    expect(harness.client.save).not.toHaveBeenCalled();
  });

  // The shortcut does not go through the Save button, so the button being
  // disabled refuses nothing. Without the same test in the save path, holding
  // Ctrl/⌘+S on an untouched note writes one save_id into the server's
  // idempotency ledger per keypress and reports each one as a success.
  it("saves nothing when the note is untouched", async () => {
    const harness = mount();
    await harness.controller.load();
    expect(harness.saveButton().disabled).toBe(true);

    const event = new KeyboardEvent("keydown", { key: "s", ctrlKey: true, cancelable: true, bubbles: true });
    document.body.dispatchEvent(event);
    await Promise.resolve();

    expect(harness.client.save).not.toHaveBeenCalled();
    expect(harness.status()).not.toContain("Saving");
  });

  // Ctrl/⌘+Shift+S is somebody else's chord — the browser's, an extension's —
  // and this listener is in the capture phase, so claiming it would swallow it
  // and do something the person did not ask for.
  it("leaves Ctrl/⌘+Shift+S to whatever else claimed it", async () => {
    const harness = mount();
    await harness.controller.load();
    harness.surface().type("edited\n");

    const event = new KeyboardEvent("keydown", {
      key: "s",
      ctrlKey: true,
      shiftKey: true,
      cancelable: true,
      bubbles: true,
    });
    document.body.dispatchEvent(event);

    expect(event.defaultPrevented).toBe(false);
    expect(harness.client.save).not.toHaveBeenCalled();
  });

  it("stops listening once destroyed", async () => {
    const harness = mount();
    await harness.controller.load();
    harness.surface().type("edited\n");
    harness.controller.destroy();

    document.body.dispatchEvent(new KeyboardEvent("keydown", { key: "s", ctrlKey: true, bubbles: true }));
    expect(harness.client.save).not.toHaveBeenCalled();
  });
});

it("retries a failed preview with the current draft without saving or editing it", async () => {
  const harness = mount({}, 10000);
  await harness.controller.load();
  await vi.waitFor(() => expect(harness.elements.preview?.innerHTML).toContain("Alpha body"));
  harness.client.preview.mockRejectedValueOnce(new Error("Offline."));
  harness.surface().type("draft one");
  harness.controller.panes.select("preview");
  const retry = document.querySelector<HTMLButtonElement>("[data-source-preview-retry]")!;
  await vi.waitFor(() => expect(retry.hidden).toBe(false));
  expect(harness.elements.previewStatus?.textContent).toContain("previous preview is outdated");
  expect(harness.elements.preview?.innerHTML).toContain("Alpha body");
  harness.surface().type("draft two");
  let resolve!: (html: string) => void;
  harness.client.preview.mockImplementationOnce(() => new Promise<string>(done => { resolve = done; }));
  retry.focus();
  retry.click();
  retry.click();
  expect(harness.client.preview).toHaveBeenCalledTimes(3);
  expect(harness.client.preview).toHaveBeenLastCalledWith("draft two");
  expect(retry.getAttribute("aria-disabled")).toBe("true");
  resolve("<p>draft two</p>");
  await vi.waitFor(() => expect(retry.hidden).toBe(true));
  expect(harness.elements.preview?.innerHTML).toBe("<p>draft two</p>");
  expect(harness.surface().getValue()).toBe("draft two");
  expect(harness.controller.dirty()).toBe(true);
  expect(harness.client.save).not.toHaveBeenCalled();
  expect(document.activeElement).toBe(document.querySelector('[data-source-pane="preview"]'));
});

it("offers retry after an initial preview failure and ignores replies after teardown", async () => {
  const harness = mount({ preview: vi.fn().mockRejectedValue(new Error("Offline.")) });
  await harness.controller.load();
  const retry = document.querySelector<HTMLButtonElement>("[data-source-preview-retry]")!;
  await vi.waitFor(() => expect(retry.hidden).toBe(false));
  expect(harness.elements.previewStatus?.textContent).toContain("No preview is available");
  let resolve!: (html: string) => void;
  harness.client.preview.mockImplementationOnce(() => new Promise<string>(done => { resolve = done; }));
  retry.click();
  expect(harness.client.preview).toHaveBeenLastCalledWith(LOADED);
  harness.controller.destroy();
  resolve("<p>late</p>");
  await Promise.resolve();
  expect(harness.elements.preview?.innerHTML).toBe("");
});
