import { setSourceStatus } from "./source-status";
import type { StatusKind } from "./status";
import { createEditorTitle } from "./source-editor-title";
import { formatSource, type SourceFormatCommand, type SourceFormatOptions } from "./source-format";
import type { PreviewScheduler } from "./source-preview";
import { createPreviewPanel } from "./source-preview-panel";
import {
  SourceReadError,
  notFoundCode,
  type SourceClient,
} from "./source-save";
import { initSourcePanes, type SourcePanes } from "./source-editor-panes";
import { createDraftKeeper, type DraftKeeper } from "./source-editor-drafts";
import type { AttachmentClaims } from "./source-save";
import type { DraftOffer, VaultReading } from "./source-draft-recovery";
import type { SourceDraftStore } from "./source-draft";
import type { SourceEditorConfig, SourceEditorElements } from "./source-editor-elements";
import type { SourceSurface, SourceSurfaceFactory } from "./source-surface";
import { errorMessage, runSave } from "./source-editor-save";
import { showConflict, wireConflict, renderConflictComparison } from "./source-editor-conflict";

// The editor's behaviour, written against the abstract surface.
//
// This is where the session's two correctness obligations live. The first is
// the revision contract: the bytes and the revision come from one read of
// /api/source, every save echoes the revision it was planned against, and the
// answer to a save re-pins it — including the answers that are not successes.
// The second is that a stale preview must never be shown, which is
// source-preview.ts's job and is wired here.
//
// Nothing below touches CodeMirror. That is what lets the whole of it run under
// jsdom against a fake surface, and it is why the browser suite has only one
// editor spec to carry rather than the entire behaviour.

export type SourceEditorDeps = {
  elements: SourceEditorElements;
  config: SourceEditorConfig;
  client: SourceClient;
  createSurface: SourceSurfaceFactory;
  previewDelayMs?: number;
  doc?: Document;
  // drafts is the browser-local copy of unsaved text. It is optional, and a null
  // store is a fully working editor with no safety net rather than a broken one:
  // a browser that refuses storage must not cost somebody their editor as well.
  drafts?: SourceDraftStore | null;
  draftDelayMs?: number;
  now?: () => number;
  navigate?: (url: string) => void;
  // claims is whatever is holding staged uploads for this page. It is optional
  // on the same terms as `drafts`: a page with no attachment panel saves exactly
  // as it did before, carrying no owner and no ids.
  claims?: AttachmentClaims | null;
};

export type SourceEditorController = {
  load(): Promise<void>;
  save(): Promise<void>;
  openSearch(): void;
  runCommand(command: SourceFormatCommand, options?: SourceFormatOptions): void;
  // insertText puts text where the cursor is, replacing the selection, and
  // leaves the cursor after it. It is how an attachment's link reaches the note,
  // and it goes through the surface's own edit rather than through setValue so
  // the person's undo history survives it.
  insertText(text: string): void;
  panes: SourcePanes;
  dirty(): boolean;
  destroy(): void;
};

export type EditorState = {
  surface: SourceSurface | null;
  // baseRevision is the revision the next save is planned against. It moves on
  // every definitive answer from the server and on nothing else.
  baseRevision: string;
  // savedContent is what the vault is believed to hold. Dirtiness is a
  // comparison against it rather than a flag, so undoing back to the loaded
  // text correctly reports clean.
  savedContent: string;
  // pendingSave survives a request whose outcome is unknown: the attempt's id
  // and the exact bytes it carried. Reusing the id is what makes a retry a
  // replay instead of a second write, and the bytes are held with it because
  // that is only true of the *same* submission — the ledger records an id
  // against a digest and refuses different content under it
  // (planSourceSaveLedger in internal/noteops/source_ledger.go). A request the
  // server answered clears the pair, because the next save is a new attempt.
  pendingSave: { id: string; content: string } | null;
  saving: boolean;
};

export const createSourceEditorController = (
  deps: SourceEditorDeps
): SourceEditorController => {
  const { elements, client } = deps;
  const ownerDocument = deps.doc ?? elements.panel.ownerDocument;
  const title = createEditorTitle(ownerDocument);
  const panes = initSourcePanes(elements);
  const state: EditorState = {
    surface: null,
    baseRevision: "",
    savedContent: "",
    pendingSave: null,
    saving: false,
  };

  const setStatus = (message: string, kind: StatusKind = "", idle = false): void => {
    setSourceStatus(elements.status, message, kind, idle);
  };

  const preview = createPreviewPanel(elements, (text) => client.preview(text),
    () => state.surface?.getValue() ?? "", deps.previewDelayMs);

  const dirty = (): boolean =>
    state.surface !== null && state.surface.getValue() !== state.savedContent;

  // The button offers exactly what runSave will accept, including the exception:
  // an unanswered attempt is still worth pressing Save over even when the text
  // matches what was last adopted, because its bytes may be on disk. Undoing back
  // to the saved text is the natural reaction to a save that did not go through,
  // and a button that disabled itself there would say "nothing unsaved" over an
  // attempt whose fate nobody knows, leaving Ctrl/⌘+S as the only way to settle it.
  const refreshSaveButton = (): void => {
    title.update(dirty() || state.pendingSave !== null);
    if (elements.save) {
      elements.save.disabled = state.saving || (!dirty() && state.pendingSave === null);
    }
  };

  const drafts = createKeeper(deps, state, {
    preview,
    panes,
    refreshSaveButton,
    dirty,
    setStatus,
  });

  // adopt records what the vault is believed to hold. `settled` says whether the
  // browser's own copy is finished with too: true when this editor is the reason
  // the vault holds it — a save, or taking over the other writer's version from
  // the conflict panel — and false for a load, where the record in storage is
  // the one the recovery panel is about to ask about. Dropping it there would be
  // answering for the person, and after a reload that record is precisely the
  // text the page exists to hand back.
  const adopt = (content: string, revision: string, settled = true): void => {
    state.savedContent = content;
    state.baseRevision = revision;
    state.pendingSave = null;
    if (settled) {
      drafts.adopted(content, revision);
    }
    refreshSaveButton();
  };

  const ctx: SaveContext = { setStatus, adopt, refreshSaveButton, preview, panes, drafts };
  let unbind: (() => void) | null = null;

  const controller: SourceEditorController = {
    panes,
    dirty,

    load: (): Promise<void> => runLoad(deps, state, ctx),

    save: (): Promise<void> => runSave(deps, state, ctx),
    openSearch: (): void => openEditorSearch(state.surface, panes),

    runCommand: (command: SourceFormatCommand, options?: SourceFormatOptions): void =>
      applyCommand(state.surface, command, options),

    insertText: (text: string): void => insertAtCursor(state.surface, text),

    destroy(): void {
      // Flushed before anything is torn down: destroy() runs on a page that is
      // going away, and the debounce may be holding the last few keystrokes.
      drafts.flush();
      drafts.dispose();
      preview.dispose();
      state.surface?.destroy();
      state.surface = null;
      unbind?.();
      title.dispose();
    },
  };

  unbind = wireEditor(ownerDocument, elements, state, controller, ctx);

  return controller;
};

const openEditorSearch = (surface: SourceSurface | null, panes: SourcePanes): void => {
  if (!surface) return;
  panes.select("edit");
  surface.openSearch();
};

const applyCommand = (
  surface: SourceSurface | null,
  command: SourceFormatCommand,
  options: SourceFormatOptions | undefined
): void => {
  if (!surface) {
    return;
  }
  surface.applyEdit(formatSource(surface.getValue(), surface.getSelection(), command, options));
  surface.focus();
};

// insertAtCursor replaces the selection with the text and leaves the cursor
// after it — the behaviour of typing it, which is what somebody who just dropped
// a file into the note expects to have happened. It goes through applyEdit
// rather than setValue so the edit joins the surface's undo history instead of
// replacing the document under it.
const insertAtCursor = (surface: SourceSurface | null, text: string): void => {
  if (!surface) {
    return;
  }
  const selection = surface.getSelection();
  const at = selection.from + text.length;
  surface.applyEdit({ ...selection, insert: text, selectionFrom: at, selectionTo: at });
  surface.focus();
};

// Everything that points at the finished controller, which is why it runs last
// and in one place: the key bindings hand back the unbind destroy() owes the
// document, and the pane listener keeps the preview from rendering text nobody
// is looking at.
const wireEditor = (
  ownerDocument: Document,
  elements: SourceEditorElements,
  state: EditorState,
  controller: SourceEditorController,
  ctx: SaveContext
): (() => void) => {
  wireControls(elements, controller);
  wireConflict(elements, state, controller, ctx);
  ctx.panes.onSelect((pane) => {
    if (pane === "preview") {
      ctx.preview.flush(state.surface?.getValue() ?? "");
    }
  });
  return bindGlobalKeys(ownerDocument, controller);
};

// createKeeper states the seam between the editor and the draft machinery in
// one place: five things the keeper may ask of this editor, and not one of them
// is a save. `restore` is the only one that changes what is on screen, and it
// goes through restoreDraft, which makes no request either.
const createKeeper = (
  deps: SourceEditorDeps,
  state: EditorState,
  ctx: RestoreContext & {
    dirty(): boolean;
    setStatus(message: string, kind: StatusKind): void;
  }
): DraftKeeper => {
  const { elements } = deps;
  const view = (deps.doc ?? elements.panel.ownerDocument).defaultView;
  return createDraftKeeper({
    store: deps.drafts ?? null,
    elements,
    config: deps.config,
    client: deps.client,
    host: {
      currentContent: () => state.surface?.getValue() ?? "",
      baseRevision: () => state.baseRevision,
      // No surface is a load that never produced an editor, and it is reported
      // as "unknown" rather than as clean: a page whose read failed has compared
      // nothing, and the keeper must not conclude from it that the vault holds
      // what is kept.
      status: () => (state.surface === null ? "unknown" : ctx.dirty() ? "unsaved" : "held"),
      saving: () => state.saving,
      restore: (offer) => restoreDraft(elements, state, ctx, offer),
      setStatus: ctx.setStatus,
    },
    view,
    now: deps.now ?? (() => Date.now()),
    delayMs: deps.draftDelayMs,
    navigate: deps.navigate ?? ((url) => view?.location.assign(url)),
    claims: deps.claims,
  });
};

// The save shortcut is bound on the document rather than only in the editor's
// keymap, because the toolbar, the tabs and the conflict buttons are all
// focusable and Ctrl/⌘+S from any of them has to mean the same thing — and it
// has to preventDefault, or the browser offers to save the HTML page instead.
// Capture phase for the same reason: nothing else may claim it first.
const bindGlobalKeys = (
  ownerDocument: Document,
  controller: SourceEditorController
): (() => void) => {
  const onKeyDown = (event: KeyboardEvent): void => {
    // Alt and Shift are excluded rather than ignored: Ctrl/⌘+Alt+S and
    // Ctrl/⌘+Shift+S are bound elsewhere — by browsers, by extensions, by the
    // operating system — and this listener runs in the capture phase, so
    // treating them as Save would swallow a chord that was never meant for the
    // page and silently do something else with it.
    if (
      event.key.toLowerCase() !== "s" ||
      !(event.metaKey || event.ctrlKey) ||
      event.altKey ||
      event.shiftKey
    ) {
      return;
    }
    event.preventDefault();
    void controller.save();
  };

  const onBeforeUnload = (event: BeforeUnloadEvent): void => {
    if (!controller.dirty()) {
      return;
    }
    // The text exists only in this tab. The browser decides what to show; all a
    // page can do is ask for the prompt, and both spellings are needed because
    // browsers disagree about which one arms it.
    event.preventDefault();
    event.returnValue = "";
  };

  ownerDocument.addEventListener("keydown", onKeyDown, true);
  ownerDocument.defaultView?.addEventListener("beforeunload", onBeforeUnload);
  return () => {
    ownerDocument.removeEventListener("keydown", onKeyDown, true);
    ownerDocument.defaultView?.removeEventListener("beforeunload", onBeforeUnload);
  };
};

const mountSurface = (
  deps: SourceEditorDeps,
  state: EditorState,
  content: string,
  onChange: () => void
): void => {
  state.surface?.destroy();
  const surface = deps.createSurface(deps.elements.surfaceHost, content);
  surface.onChange(onChange);
  state.surface = surface;
  if (deps.elements.find) deps.elements.find.disabled = false;
};

export type SaveContext = {
  setStatus(message: string, kind?: StatusKind, idle?: boolean): void;
  adopt(content: string, revision: string, settled?: boolean): void;
  refreshSaveButton(): void;
  preview: PreviewScheduler;
  panes: SourcePanes;
  drafts: DraftKeeper;
};

const runLoad = async (
  deps: SourceEditorDeps,
  state: EditorState,
  ctx: SaveContext
): Promise<void> => {
  ctx.setStatus("Loading the note's source…", "is-loading");
  // Read before anything adopts or drops: adopting the loaded note clears this
  // tab's own slot, and after a reload that slot holds precisely the draft this
  // page exists to offer back.
  const kept = ctx.drafts.kept();
  // A reload over a live editor leaves it editable while the read is in
  // flight, so what is on screen now is compared with what comes back.
  const before = state.surface?.getValue() ?? null;
  try {
    const loaded = await deps.client.read();
    if (before !== null && state.surface !== null && state.surface.getValue() !== before) {
      keepTypedOverReload(deps.elements, state, ctx, loaded);
      return;
    }
    mountSurface(deps, state, loaded.content, () => {
      ctx.refreshSaveButton();
      refreshEditStatus(deps.elements, state, ctx.setStatus);
      // The preview is scheduled on every edit regardless of which pane is
      // showing. On a wide screen both are, so this is simply correct; on a
      // narrow one it costs a debounced request for a hidden pane, and the
      // alternative — asking matchMedia which layout is in force — puts a
      // breakpoint that belongs in css/responsive.css into this file, where
      // the two would drift apart silently.
      ctx.preview.schedule(state.surface?.getValue() ?? "");
      ctx.drafts.keep();
    });
    ctx.adopt(loaded.content, loaded.revision, false);
    ctx.setStatus("Ready to edit.", "", true);
    ctx.preview.flush(loaded.content);
    ctx.drafts.present(kept, { kind: "loaded", revision: loaded.revision, content: loaded.content });
  } catch (error: unknown) {
    ctx.setStatus(errorMessage(error, "The note's source could not be loaded."), "is-error");
    // A load that failed is the one moment a kept draft is most valuable and
    // most easily thrown away. Nothing is compared, nothing is dropped, and the
    // offer says which of the two failures this was.
    ctx.drafts.present(kept, readingFor(error));
  }
};

// keepTypedOverReload is a reload that lost the race with the keyboard. The
// text typed while the read was in flight exists only on screen, so it stays
// there, and the base revision stays where it was: the vault has moved on, and
// the next save goes through the server's conflict refusal rather than over the
// top of it. The vault's version goes on the conflict panel now, so the two
// choices are on offer before the person has to find them by saving.
const keepTypedOverReload = (
  elements: SourceEditorElements,
  state: EditorState,
  ctx: SaveContext,
  loaded: { content: string; revision: string }
): void => {
  const typed = state.surface?.getValue() ?? "";
  if (loaded.content === state.savedContent) {
    // Nothing moved after all, so the reload has nothing to tell the editor.
    ctx.refreshSaveButton();
    refreshEditStatus(elements, state, ctx.setStatus);
    return;
  }
  showConflict(elements, loaded.content, loaded.revision, typed);
  ctx.panes.select("edit");
  ctx.refreshSaveButton();
  ctx.setStatus(
    "The note changed on disk while you were typing. Your text is still here; choose a version below or save to compare.",
    "is-warning"
  );
};

// readingFor separates the note being gone from nobody having answered.
//
// They arrive at this catch identically — one rejected promise — and mean
// opposite things: `not_found` is the vault saying the note is not there, which
// is a recovery choice, while anything else is silence, which entitles this page
// to conclude nothing at all about the note or about the draft.
const readingFor = (error: unknown): VaultReading =>
  error instanceof SourceReadError && error.code === notFoundCode
    ? { kind: "missing" }
    : { kind: "unreachable" };

type RestoreContext = {
  preview: PreviewScheduler;
  panes: SourcePanes;
  refreshSaveButton(): void;
  setStatus(message: string, kind?: StatusKind, idle?: boolean): void;
};

// restoreDraft puts kept text back in the editor, and nothing else. No request
// is made and no revision is adopted: the editor ends up exactly as it would if
// the person had typed the text again.
//
// A stale draft gets one extra thing, and it is the important one. Its base
// revision is re-pinned to the revision it was *written* against, which is
// simply true — and it is what makes the next save go through the server's own
// conflict refusal instead of quietly overwriting whatever landed in the note
// since. The vault's current version goes on the conflict panel at the same
// time, so the two choices on offer are the two that already exist: save mine
// over it, or discard mine and load the note.
const restoreDraft = (
  elements: SourceEditorElements,
  state: EditorState,
  ctx: RestoreContext,
  offer: DraftOffer
): void => {
  state.surface?.setValue(offer.draft.content);
  if (offer.kind === "stale") {
    state.baseRevision = offer.draft.baseRevision;
    showConflict(elements, offer.vaultContent, offer.vaultRevision, offer.draft.content);
    ctx.panes.select("edit");
  }
  ctx.refreshSaveButton();
  ctx.preview.flush(offer.draft.content);
  // setValue is not an edit, so nothing else will say the restored text is
  // unsaved; "Ready to edit." would otherwise stay over a dirty editor.
  refreshEditStatus(elements, state, ctx.setStatus);
};

const wireControls = (
  elements: SourceEditorElements,
  controller: SourceEditorController
): void => {
  for (const button of elements.commands) {
    button.addEventListener("click", () => {
      const command = button.getAttribute("data-source-command") as SourceFormatCommand | null;
      if (command) {
        controller.runCommand(command, { headingLevel: Number(button.getAttribute("data-source-heading-level")) || 1 });
      }
    });
  }
  elements.save?.addEventListener("click", () => void controller.save());
  elements.find?.addEventListener("click", () => controller.openSearch());
};

const statusHeld = (elements: SourceEditorElements, state: EditorState): boolean => {
  if (state.saving || state.pendingSave) return true;
  const conflictShown = elements.conflict ? !elements.conflict.hidden : false;
  return conflictShown && (elements.status?.matches(".is-error, .is-warning") ?? false);
};

// refreshEditStatus says whether the text is saved, from the save state rather
// than from whatever the line last said. An in-flight or unanswered save owns
// the line, and so does an error or warning while the conflict panel it
// explains is still up; any other outcome is history once the text changes.
export const refreshEditStatus = (
  elements: SourceEditorElements,
  state: EditorState,
  setStatus: (message: string, kind?: StatusKind, idle?: boolean) => void
): void => {
  renderConflictComparison(elements, state.surface?.getValue() ?? "");
  if (statusHeld(elements, state)) return;
  if (state.surface?.getValue() === state.savedContent) {
    setStatus("No unsaved changes.", "", true);
  } else {
    setStatus("Unsaved changes.");
  }
};
