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
import type { SourceClient } from "./source-save";
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

const mount = (overrides: Partial<SourceClient> = {}, previewDelayMs = 0): Harness => {
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
  ({ code: "", message: "", revision: "", content: "", changed: false, ...payload }) as SourceSaveResponse;

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
  it("puts the note's bytes in the surface and starts clean", async () => {
    const harness = mount();
    await harness.controller.load();

    expect(harness.surface().getValue()).toBe(LOADED);
    expect(harness.controller.dirty()).toBe(false);
    expect(harness.saveButton().disabled).toBe(true);
    expect(harness.status()).toContain("Alpha.md");
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

    // Undoing back to the loaded text is clean, which a boolean flag set on the
    // first keystroke would get wrong.
    harness.surface().type(LOADED);
    expect(harness.controller.dirty()).toBe(false);
    expect(harness.saveButton().disabled).toBe(true);
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

    harness.elements.conflictDiscard?.click();
    expect(harness.saveButton().disabled).toBe(true);
    expect(harness.elements.conflict?.hasAttribute("data-source-conflict-revision")).toBe(false);
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
  const pinMoved = (): Harness =>
    mount({
      save: vi.fn().mockResolvedValue(
        saveFailure({
          ok: false,
          code: "conflict",
          message: "source: the configuration changed while saving Alpha.md, so nothing was written",
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
    expect(harness.status()).toContain("the configuration changed");
    expect(harness.elements.status?.classList.contains("is-error")).toBe(true);
    expect(harness.surface().getValue()).toContain("my draft\n");
    expect(harness.controller.dirty()).toBe(true);
  });

  // The server answered, so pressing Save again is a fresh attempt against the
  // same, still-current revision.
  it("leaves the save planned against the revision it was already planned against", async () => {
    const harness = pinMoved();
    await harness.controller.load();
    harness.surface().type("my draft\n");

    await harness.controller.save();
    await harness.controller.save();
    expect(harness.client.save.mock.calls[1][0].revision).toBe("r1");
    const [first, second] = harness.client.save.mock.calls.map((call) => call[0].saveID);
    expect(second).not.toBe(first);
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

  it("stops listening once destroyed", async () => {
    const harness = mount();
    await harness.controller.load();
    harness.surface().type("edited\n");
    harness.controller.destroy();

    document.body.dispatchEvent(new KeyboardEvent("keydown", { key: "s", ctrlKey: true, bubbles: true }));
    expect(harness.client.save).not.toHaveBeenCalled();
  });
});
