import { classifySaveResponse, newSaveID, type SaveOutcome } from "./source-save";
import type { SourceEditorDeps, EditorState, SaveContext } from "./source-editor-controller";
import type { SourceEditorElements } from "./source-editor-elements";
import { hideConflict, showConflict } from "./source-editor-conflict";

export const runSave = async (
  deps: SourceEditorDeps,
  state: EditorState,
  ctx: SaveContext
): Promise<void> => {
  const surface = state.surface;
  if (!surface || state.saving) {
    return;
  }
  const content = surface.getValue();
  // The same refusal the Save button expresses by being disabled. It has to be
  // here rather than only there because Ctrl/⌘+S does not go through the
  // button: without it, the shortcut on an untouched note mints a save_id,
  // writes a ledger entry, takes the vault lock and reports success for a save
  // nobody asked for — once per keypress.
  //
  // An unanswered attempt is the exception. Its bytes may be on disk, so text
  // that merely matches what this editor last adopted is still worth sending;
  // resumableSaveID decides whether that is a replay or a new write.
  if (content === state.savedContent && state.pendingSave === null) {
    return;
  }
  const tooLarge = oversizeMessage(content, deps.config.maxBytes);
  if (tooLarge !== "") {
    ctx.setStatus(tooLarge, "is-error");
    return;
  }

  // Written before the request, not after it. A save is the likeliest moment for
  // the tab to be closed, the network to drop or the server to restart, and the
  // debounce may still be holding the last few keystrokes of what is about to be
  // sent.
  ctx.drafts.flush();

  state.saving = true;
  const base = state.baseRevision;
  state.pendingSave = { id: resumableSaveID(state.pendingSave, content), content };
  ctx.refreshSaveButton();
  ctx.setStatus("Saving…", "is-loading");
  const waiting = window.setTimeout(() =>
    ctx.setStatus("Save not confirmed yet. Your text is still in the editor.", "is-loading"), 500);
  try {
    const response = await deps.client.save({
      content,
      revision: base,
      saveID: state.pendingSave.id,
      owner: deps.claims?.owner,
      attachments: deps.claims?.pending(),
    });
    // Settled before the outcome is acted on, and off the response rather than
    // off the request: the envelope names what the vault holds, which is the
    // only thing that entitles this page to stop owing an upload.
    deps.claims?.committed(response.attachments);
    applyOutcome(classifySaveResponse(response, base), content, state, ctx, deps.elements);
  } catch {
    // The request never produced an answer, so whether the bytes landed is
    // unknown. pendingSaveID is deliberately kept: pressing Save again
    // resubmits the same idempotency key, which the server replays rather than
    // writing twice.
    ctx.setStatus(
      "Save not confirmed. Your text is still in the editor. Press Save to retry safely.",
      "is-error"
    );
  } finally {
    window.clearTimeout(waiting);
    state.saving = false;
    ctx.refreshSaveButton();
  }
};

// resumableSaveID decides whether this save continues the pending attempt or
// starts a new one.
//
// An unanswered attempt keeps its id so that pressing Save again is a replay
// rather than a second write — but that is only true of a resubmission of the
// same bytes. Once the person has typed on, the text they are sending is not
// the text the ledger may have recorded under that id, and reusing it would be
// answered with the ledger's own refusal ("retry with a fresh save id"), which
// is a sentence about an internal identifier nobody in the browser can act on.
// Different bytes are a different submission, so they get a different id and
// the base revision goes back to being what protects them.
const resumableSaveID = (
  pending: { id: string; content: string } | null,
  content: string
): string => (pending && pending.content === content ? pending.id : newSaveID());

const applyOutcome = (
  outcome: SaveOutcome,
  submitted: string,
  state: EditorState,
  ctx: SaveContext,
  elements: SourceEditorElements
): void => {
  switch (outcome.kind) {
    case "saved":
      ctx.adopt(submitted, outcome.revision);
      hideConflict(elements);
      // The other tabs on this origin are told, and told only. Each of them is
      // holding its own unsaved text against its own pinned revision, so this is
      // a notice that the note has moved — never an instruction to adopt it.
      ctx.drafts.announce(outcome.revision);
      ctx.setStatus(state.surface?.getValue() === submitted
        ? "Saved to vault." : "Saved to vault. Newer edits are still unsaved.", "is-success");
      return;
    case "landed":
      // The bytes are on disk and the content history does not cover them. The
      // revision has to move anyway, or the next save is planned against one
      // this save already replaced.
      ctx.adopt(submitted, outcome.revision);
      // And the panel goes for the same reason it goes on a success: the
      // version it holds is now older than what the note contains, so
      // "overwrite" would offer to re-pin to a superseded revision and
      // "discard" would offer to load bytes this save has already replaced.
      // A refusal that is *not* a write is deliberately left alone below — the
      // other writer's version is still the newest thing anybody has seen, and
      // taking it off screen over an unrelated error would lose the only copy.
      hideConflict(elements);
      ctx.drafts.announce(outcome.revision);
      ctx.setStatus(outcome.message, "is-error");
      return;
    case "missing":
      // The note this page was opened for is gone, so the refusal is final and
      // the attempt is settled — but the text on screen now has nowhere to go
      // back to. It is offered as a new note at the path this editor was opened
      // for, and it is a button: nothing is written by arriving here.
      state.pendingSave = null;
      ctx.setStatus(outcome.message, "is-error");
      ctx.drafts.offerRecreate(submitted);
      return;
    case "conflict":
      state.pendingSave = null;
      showConflict(elements, outcome.content, outcome.revision);
      ctx.panes.select("edit");
      ctx.setStatus(outcome.message, "is-error");
      return;
    case "failed":
      // Only a refusal this API *sent* clears the attempt. An answer that was
      // never this envelope leaves the write's fate unknown, and dropping the
      // id there would turn the retry into a second write of the same text
      // against a revision the first one may already have replaced.
      if (outcome.answered) {
        state.pendingSave = null;
      }
      ctx.setStatus(outcome.message, "is-error");
  }
};

// oversizeMessage refuses a save the server would refuse anyway, before four
// megabytes go over the wire to be told so. The limit is the server's own,
// published on the page.
const oversizeMessage = (content: string, maxBytes: number): string => {
  if (maxBytes <= 0) {
    return "";
  }
  const bytes = new TextEncoder().encode(content).length;
  return bytes > maxBytes
    ? `This note is ${bytes} bytes, over the ${maxBytes}-byte limit for a source save. Nothing was sent.`
    : "";
};

export const errorMessage = (error: unknown, fallback: string): string => {
  if (error instanceof Error && error.message.trim() !== "") {
    return error.message.trim();
  }
  return fallback;
};
