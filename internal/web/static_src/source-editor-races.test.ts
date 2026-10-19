// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import type { SourceSaveResponse } from "./api-types-source";
import { memoryDraftStorage } from "./source-draft.test-helpers";
import {
  cleanup as cleanupDrafts,
  EDITED,
  mount as mountWithDrafts,
  saveSuccess as draftSaveSuccess,
  seedResume,
} from "./source-editor-drafts.test-helpers";
import {
  cleanup,
  deferred,
  inConflict,
  LOADED,
  mount,
  saveFailure,
} from "./source-editor-controller.test-helpers";

afterEach(() => {
  cleanup();
  cleanupDrafts();
});

const reading = (content: string, revision: string) => ({
  key: "alpha",
  path: "/n/alpha",
  rel_path: "Alpha.md",
  revision,
  content,
});

describe("a reload that races the keyboard", () => {
  // A property set or an undo settles by re-reading the note while the editor
  // stays editable. Whatever is typed before the read answers exists only on
  // screen, so the reload must not replace it.
  it("keeps text typed while the read was in flight and shows both versions", async () => {
    const harness = mount();
    await harness.controller.load();
    const moved = `${LOADED}status: done\n`;
    const answer = deferred<ReturnType<typeof reading>>();
    harness.client.read.mockReturnValueOnce(answer.promise);

    const reloading = harness.controller.load();
    harness.surface().type(`${LOADED}typed during the reload\n`);
    answer.resolve(reading(moved, "r2"));
    await reloading;

    expect(harness.surface().getValue()).toBe(`${LOADED}typed during the reload\n`);
    expect(harness.controller.dirty()).toBe(true);
    expect(harness.conflict().hidden).toBe(false);
    expect(harness.elements.status?.classList.contains("is-warning")).toBe(true);
    // The base revision stays where the typed text was written, so a save is
    // refused by the server's own check instead of overwriting the new version.
    await harness.controller.save();
    expect(harness.client.save).toHaveBeenCalledWith(expect.objectContaining({ revision: "r1" }));
  });

  it("remounts as before when nothing was typed during the read", async () => {
    const harness = mount();
    await harness.controller.load();
    harness.client.read.mockResolvedValueOnce(reading(`${LOADED}moved\n`, "r2"));

    await harness.controller.load();

    expect(harness.surface().getValue()).toBe(`${LOADED}moved\n`);
    expect(harness.controller.dirty()).toBe(false);
    expect(harness.conflict().hidden).toBe(true);
  });
});

describe("the save line", () => {
  it("marks the idle states explicitly, not by their wording", async () => {
    const harness = mount();
    await harness.controller.load();
    expect(harness.elements.status?.dataset.idle).toBe("true");

    harness.surface().type(`${LOADED}more\n`);
    expect(harness.status()).toBe("Unsaved changes.");
    expect(harness.elements.status?.dataset.idle).toBe("false");

    harness.surface().type(LOADED);
    expect(harness.status()).toBe("No unsaved changes.");
    expect(harness.elements.status?.dataset.idle).toBe("true");
  });

  // An answered refusal with no panel to explain it is history once the text
  // changes; leaving it up would hide "Unsaved changes." for the rest of the
  // session.
  it("lets an edit replace a settled error", async () => {
    const harness = mount({
      save: vi.fn().mockResolvedValue(
        saveFailure({ ok: false, code: "invalid_request", message: "source: frontmatter is not valid YAML" })
      ),
    });
    await harness.controller.load();
    harness.surface().type("bad\n");
    await harness.controller.save();
    expect(harness.elements.status?.classList.contains("is-error")).toBe(true);

    harness.surface().type("fixed\n");
    expect(harness.status()).toBe("Unsaved changes.");
  });

  it("clears the conflict's error once its version is taken", async () => {
    const harness = await inConflict();
    expect(harness.elements.status?.classList.contains("is-error")).toBe(true);

    harness.elements.conflictDiscard?.click();

    expect(harness.elements.status?.classList.contains("is-error")).toBe(false);
    harness.surface().type("after the discard\n");
    expect(harness.status()).toBe("Unsaved changes.");
  });
});

describe("restoring a draft", () => {
  // setValue is not an edit, so nothing but the restore itself can say the
  // restored text is unsaved.
  it("replaces Ready to edit with Unsaved changes", async () => {
    const storage = memoryDraftStorage();
    seedResume(storage, { draftID: "tab-earlier", content: `${EDITED}mine\n` });
    const harness = mountWithDrafts({ storage, draftID: "tab-earlier" });
    await harness.controller.load();
    expect(harness.status()).toBe("Ready to edit.");

    harness.elements.recoveryRestore?.click();

    expect(harness.status()).toBe("Unsaved changes.");
    expect(harness.elements.status?.dataset.idle).toBe("false");
  });
});

describe("edits typed during a save", () => {
  // The vault holds what was sent, not what is on screen: the keystrokes that
  // arrived while the request was in flight exist only in the editor, and the
  // save's answer must not take their only browser copy with it.
  it("keeps the newer text in storage, against the saved revision", async () => {
    const answer = deferred<SourceSaveResponse>();
    const harness = mountWithDrafts({ client: { save: vi.fn(() => answer.promise) } });
    await harness.controller.load();
    harness.surface().type(EDITED);
    const saving = harness.controller.save();
    harness.surface().type(`${EDITED}more\n`);
    answer.resolve(draftSaveSuccess({ revision: "r2" }));
    await saving;

    expect(harness.status()).toBe("Saved to vault. Newer edits are still unsaved.");
    expect(harness.drafts()).toEqual([
      expect.objectContaining({ content: `${EDITED}more\n`, baseRevision: "r2" }),
    ]);
  });
});
