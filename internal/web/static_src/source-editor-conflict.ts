import { renderTextComparison } from "./text-comparison";
import type { EditorState, SaveContext, SourceEditorController } from "./source-editor-controller";
import type { SourceEditorElements } from "./source-editor-elements";

export const wireConflict = (
  elements: SourceEditorElements,
  state: EditorState,
  controller: SourceEditorController,
  ctx: SaveContext
): void => {
  // Both buttons are declined while a save is in flight. The panel stays up for
  // the duration of a request — the two states genuinely overlap — so either
  // can be clicked over a save that has not answered yet, and each of them
  // moves `baseRevision` and tears the panel down. Doing that and *then* being
  // refused by runSave is the worst of both: the editor is left pinned to a
  // revision whose bytes are no longer on screen anywhere, the answer still to
  // come adopts over the top of it, and the choice the panel was asking about
  // was never made. So the click is declined whole rather than half-applied.
  const settled = (): boolean => !state.saving;

  elements.conflictOverwrite?.addEventListener("click", () => {
    if (!settled()) {
      return;
    }
    // Re-pin to the revision the server reported and submit the same text
    // again. This is the one place a revision moves without the vault's bytes
    // having been read here — which is exactly what "overwrite" means. The
    // attempt id was already cleared by the refusal itself: a 409 is the server
    // saying it wrote nothing, so there is no unanswered attempt left to
    // resume and the overwrite is a new one.
    state.baseRevision = conflictRevision(elements);
    hideConflict(elements);
    void controller.save();
  });

  elements.conflictDiscard?.addEventListener("click", () => {
    if (!settled()) {
      return;
    }
    // Adopting is what the vault's version *is* once it is in the editor: the
    // text, the revision and the attempt id move together, and the Save button
    // goes back to disabled because there is now nothing unsaved. Setting the
    // three by hand and leaving the button armed is how a discarded edit ends
    // up looking dirty.
    const incoming = elements.conflictSource?.textContent ?? "";
    state.surface?.setValue(incoming);
    ctx.adopt(incoming, conflictRevision(elements));
    hideConflict(elements);
    ctx.preview.flush(incoming);
    // The refusal that raised the panel is answered, so its error goes too.
    ctx.setStatus("Loaded the note's current version. No unsaved changes.", "is-success");
  });
};

// showConflict parks the vault's current version — bytes and revision together
// — on the panel itself. Both choices below need it: overwriting re-pins to
// that revision and resubmits, discarding adopts both. Keeping them on the
// element rather than in EditorState is what lets a test read back exactly what
// the person is being shown.
export const showConflict = (
  elements: SourceEditorElements,
  content: string,
  revision: string,
  draft = ""
): void => {
  if (!elements.conflict) {
    return;
  }
  if (elements.conflictSource) {
    elements.conflictSource.textContent = content;
  }
  elements.conflict.setAttribute("data-source-conflict-revision", revision);
  elements.conflict.hidden = false;
  renderConflictComparison(elements, draft);
};

export const hideConflict = (elements: SourceEditorElements): void => {
  if (elements.conflict) {
    elements.conflict.hidden = true;
    elements.conflict.removeAttribute("data-source-conflict-revision");
  }
};

const conflictRevision = (elements: SourceEditorElements): string =>
  elements.conflict?.getAttribute("data-source-conflict-revision") ?? "";

export const renderConflictComparison = (elements: SourceEditorElements, draft: string): void => {
  if (elements.conflict?.hidden) return;
  const host = elements.conflict?.querySelector<HTMLElement>("[data-source-conflict-comparison]");
  if (host) renderTextComparison(host, elements.conflictSource?.textContent ?? "", draft, "Current note", "Your draft");
};
