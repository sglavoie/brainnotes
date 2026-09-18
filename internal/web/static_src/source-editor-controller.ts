import { setStatusText, type StatusKind } from "./status";
import { formatSource, type SourceFormatCommand, type SourceFormatOptions } from "./source-format";
import { createPreviewScheduler, type PreviewScheduler } from "./source-preview";
import { classifySaveResponse, newSaveID, type SaveOutcome, type SourceClient } from "./source-save";
import { initSourcePanes, type SourcePanes } from "./source-editor-panes";
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

  const refreshSaveButton = (): void => {
    if (elements.save) {
      elements.save.disabled = state.saving || !dirty();
    }
  };

  const adopt = (content: string, revision: string): void => {
    state.savedContent = content;
    state.baseRevision = revision;
    state.pendingSave = null;
    refreshSaveButton();
  };

  const ctx: SaveContext = { setStatus, adopt, refreshSaveButton, preview, panes };
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
      preview.dispose();
      state.surface?.destroy();
      state.surface = null;
      unbind?.();
    },
  };

  unbind = bindGlobalKeys(ownerDocument, controller);

  wireControls(elements, controller);
  wireConflict(elements, state, controller, ctx);
  panes.onSelect((pane) => {
    if (pane === "preview") {
      preview.flush(state.surface?.getValue() ?? "");
    }
  });

  return controller;
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
    if (event.key.toLowerCase() !== "s" || !(event.metaKey || event.ctrlKey) || event.altKey) {
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
  adopt(content: string, revision: string): void;
  refreshSaveButton(): void;
  preview: PreviewScheduler;
  panes: SourcePanes;
};

const runLoad = async (
  deps: SourceEditorDeps,
  state: EditorState,
  ctx: SaveContext
): Promise<void> => {
  ctx.setStatus("Loading the note's source…", "is-loading");
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
    });
    ctx.adopt(loaded.content, loaded.revision);
    ctx.setStatus(`${deps.config.relPath} loaded. Nothing is written until you save.`);
    ctx.preview.flush(loaded.content);
  } catch (error: unknown) {
    ctx.setStatus(errorMessage(error, "The note's source could not be loaded."), "is-error");
  }
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
  const tooLarge = oversizeMessage(content, deps.config.maxBytes);
  if (tooLarge !== "") {
    ctx.setStatus(tooLarge, "is-error");
    return;
  }

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
      ctx.setStatus(outcome.message, "is-success");
      return;
    case "landed":
      // The bytes are on disk and the content history does not cover them. The
      // revision has to move anyway, or the next save is planned against one
      // this save already replaced.
      ctx.adopt(submitted, outcome.revision);
      ctx.setStatus(outcome.message, "is-error");
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
  elements.conflictOverwrite?.addEventListener("click", () => {
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
