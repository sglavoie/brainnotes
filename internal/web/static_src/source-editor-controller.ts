import { setStatusText, type StatusKind } from "./status";
import { formatSource, type SourceFormatCommand, type SourceFormatOptions } from "./source-format";
import { createPreviewScheduler, type PreviewScheduler } from "./source-preview";
import {
  SourceReadError,
  classifySaveResponse,
  newSaveID,
  notFoundCode,
  type SaveOutcome,
  type SourceClient,
} from "./source-save";
import { initSourcePanes, type SourcePanes } from "./source-editor-panes";
import { createDraftKeeper, type DraftKeeper } from "./source-editor-drafts";
import type { DraftOffer, VaultReading } from "./source-draft-recovery";
import type { SourceDraftStore } from "./source-draft";
import type { SourceEditorConfig, SourceEditorElements } from "./source-editor-elements";
import type { SourceSurface, SourceSurfaceFactory } from "./source-surface";

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
};

export type SourceEditorController = {
  load(): Promise<void>;
  save(): Promise<void>;
  runCommand(command: SourceFormatCommand, options?: SourceFormatOptions): void;
  panes: SourcePanes;
  dirty(): boolean;
  destroy(): void;
};

type EditorState = {
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
  const panes = initSourcePanes(elements);
  const state: EditorState = {
    surface: null,
    baseRevision: "",
    savedContent: "",
    pendingSave: null,
    saving: false,
  };

  const setStatus = (message: string, kind: StatusKind = ""): void => {
    setStatusText(elements.status, message, kind);
  };

  const preview = createPreview(elements, client, deps.previewDelayMs);

  const dirty = (): boolean =>
    state.surface !== null && state.surface.getValue() !== state.savedContent;

  // The button offers exactly what runSave will accept, including the exception:
  // an unanswered attempt is still worth pressing Save over even when the text
  // matches what was last adopted, because its bytes may be on disk. Undoing back
  // to the saved text is the natural reaction to a save that did not go through,
  // and a button that disabled itself there would say "nothing unsaved" over an
  // attempt whose fate nobody knows, leaving Ctrl/⌘+S as the only way to settle it.
  const refreshSaveButton = (): void => {
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

    runCommand(command: SourceFormatCommand, options?: SourceFormatOptions): void {
      const surface = state.surface;
      if (!surface) {
        return;
      }
      surface.applyEdit(formatSource(surface.getValue(), surface.getSelection(), command, options));
      surface.focus();
    },

    destroy(): void {
      // Flushed before anything is torn down: destroy() runs on a page that is
      // going away, and the debounce may be holding the last few keystrokes.
      drafts.flush();
      drafts.dispose();
      preview.dispose();
      state.surface?.destroy();
      state.surface = null;
      unbind?.();
    },
  };

  unbind = wireEditor(ownerDocument, elements, state, controller, ctx);

  return controller;
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
  });
};

const createPreview = (
  elements: SourceEditorElements,
  client: SourceClient,
  delayMs: number | undefined
): PreviewScheduler =>
  createPreviewScheduler({
    delayMs,
    render: (content) => client.preview(content),
    sink: {
      render: (html) => {
        if (elements.preview) {
          elements.preview.innerHTML = html;
        }
      },
      pending: () => setStatusText(elements.previewStatus, "Rendering…", "is-loading"),
      settled: (message, failed) =>
        setStatusText(
          elements.previewStatus,
          failed ? message : "Preview of the unsaved text.",
          failed ? "is-error" : ""
        ),
    },
  });

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
};

type SaveContext = {
  setStatus(message: string, kind?: StatusKind): void;
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
  try {
    const loaded = await deps.client.read();
    mountSurface(deps, state, loaded.content, () => {
      ctx.refreshSaveButton();
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
    ctx.setStatus(`${deps.config.relPath} loaded. Nothing is written until you save.`);
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
    showConflict(elements, offer.vaultContent, offer.vaultRevision);
    ctx.panes.select("edit");
  }
  ctx.refreshSaveButton();
  ctx.preview.flush(offer.draft.content);
};

const runSave = async (
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
  try {
    const response = await deps.client.save({
      content,
      revision: base,
      saveID: state.pendingSave.id,
    });
    applyOutcome(classifySaveResponse(response, base), content, state, ctx, deps.elements);
  } catch (error: unknown) {
    // The request never produced an answer, so whether the bytes landed is
    // unknown. pendingSaveID is deliberately kept: pressing Save again
    // resubmits the same idempotency key, which the server replays rather than
    // writing twice.
    ctx.setStatus(
      `${errorMessage(error, "The save could not be sent.")} Nothing was re-sent; press Save to retry.`,
      "is-error"
    );
  } finally {
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
      ctx.setStatus(outcome.message, "is-success");
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
};

const wireConflict = (
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
  });
};

// showConflict parks the vault's current version — bytes and revision together
// — on the panel itself. Both choices below need it: overwriting re-pins to
// that revision and resubmits, discarding adopts both. Keeping them on the
// element rather than in EditorState is what lets a test read back exactly what
// the person is being shown.
const showConflict = (
  elements: SourceEditorElements,
  content: string,
  revision: string
): void => {
  if (!elements.conflict) {
    return;
  }
  if (elements.conflictSource) {
    elements.conflictSource.textContent = content;
  }
  elements.conflict.setAttribute("data-source-conflict-revision", revision);
  elements.conflict.hidden = false;
};

const hideConflict = (elements: SourceEditorElements): void => {
  if (elements.conflict) {
    elements.conflict.hidden = true;
    elements.conflict.removeAttribute("data-source-conflict-revision");
  }
};

const conflictRevision = (elements: SourceEditorElements): string =>
  elements.conflict?.getAttribute("data-source-conflict-revision") ?? "";

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

const errorMessage = (error: unknown, fallback: string): string => {
  if (error instanceof Error && error.message.trim() !== "") {
    return error.message.trim();
  }
  return fallback;
};
