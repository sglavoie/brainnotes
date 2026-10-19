import { rememberSynthesisSession, synthesisResume } from "./synthesize-session";
import { mountSynthesisRecovery, synthesisRecoveryKeys } from "./synthesize-recovery";
import { confirmAction, type ConfirmAction } from "./action-confirm";
import { SynthesisRequestError } from "./api";
import { queryElement } from "./dom";
import { hasWritableClipboard } from "./clipboard";
import { relatedModeDefinitions } from "./related-model";
import { setStatusText, type StatusKind } from "./status";
import {
  createSynthesisModel,
  invalidateSynthesisContext,
  readSynthesisConfig,
  synthesisDraftError,
  synthesisFolderError,
  synthesisSelectedKeys,
  synthesisHasSavedWork,
  synthesisTitleError,
  type SynthesisConfig,
  type SynthesisModel,
} from "./synthesize-model";
import {
  renderSynthesisCandidate,
  renderSynthesisCandidates,
  renderSynthesisContext,
  renderSynthesisDestination,
  renderSynthesisPreview,
  renderSynthesisSources,
  setSynthesisFieldError,
  setSynthesisWarning,
} from "./synthesize-render";
import { createSynthesisView, type SynthesisView } from "./synthesize-view";
import type { SynthesisFetch } from "./synthesize-requests";
import type { SynthesisCandidate } from "./synthesize-sources";
import {
  clearSynthesisState,
  readSynthesisState,
  safeSynthesisStorage,
  synthesisStateFromModel,
  synthesisStorageKey,
  writeSynthesisState,
  type SynthesisStoredState,
  type SynthesisStorageLike,
} from "./synthesize-storage";
import type { RelatedMode } from "./related-types";

export type WritableClipboard = Pick<Clipboard, "writeText">;

export type SynthesisDeps = {
  draftID?: string;
  fetch?: SynthesisFetch;
  storage?: SynthesisStorageLike | null;
  clipboard?: WritableClipboard | null;
  confirm?: ConfirmAction;
  redirect?: (path: string) => void;
};

export type SynthesisController = {
  ready: Promise<void>;
};

export const errorMessage = (error: unknown, fallback: string): string =>
  error instanceof Error && error.message !== "" ? error.message : fallback;

export const errorCode = (error: unknown): string =>
  error instanceof SynthesisRequestError ? error.code : "";

export const modeLabel = (mode: RelatedMode): string =>
  relatedModeDefinitions.find((definition) => definition.mode === mode)?.label || mode;

export const SUGGESTIONS_EMPTY = "No suggestions are available in this mode.";
export const SEARCH_EMPTY = "No notes matched that search.";
export const CONTEXT_GONE =
  "The context packet has expired or is no longer valid. Rebuild it, then preview again. Your draft is unchanged.";
export const CONTEXT_SUPERSEDED =
  "The sources, purpose, or prompt changed while that packet was building, so it was discarded. Prepare the context again.";
export const PREVIEW_SUPERSEDED =
  "The note changed while that preview was building, so it was discarded. Preview again.";
export const DISCLOSURE = [
  "Generate sends the prepared context packet to your configured [agent].command.",
  "The packet contains the anchor note's complete body, exact excerpts from every selected source, and your prompt.",
  "Your prompt and that packet are retained in server memory until the context expires, is evicted to keep memory bounded, or the server restarts, and are never written to disk.",
  "The stored packet is sent to the command only after you accept this notice. The generated candidate is not stored on the server and never enters your draft on its own. Continue?",
].join("\n");
export const DISCARD_CONFIRMATION =
  "Discard the saved sources, draft, and generated candidate for this note? This cannot be undone.";
export const SAVED_STATE_STUCK =
  "This browser refused to delete the saved sources and draft for this note, so they will come back the next time you open the workbench.";

// The controller's action groups live in their own modules, so everything they
// share — the model, the view, the in-flight flags the render pass reads, and
// the handful of operations more than one group performs — is held here rather
// than in a closure only one of them could see.
export type SynthesisRuntime = {
  readonly config: SynthesisConfig;
  readonly model: SynthesisModel;
  readonly view: SynthesisView;
  readonly fetcher: SynthesisFetch;
  readonly clipboard: WritableClipboard | null;
  readonly confirmer: ConfirmAction;
  readonly redirect: (path: string) => void;
  readonly candidates: Map<string, SynthesisCandidate>;
  suggestionEntries: Record<RelatedMode, SynthesisCandidate[] | null>;
  searchEntries: SynthesisCandidate[] | null;
  generating: boolean;
  previewing: boolean;
  applying: boolean;
  // Validation messages stay hidden until the user asks for something that needs
  // valid fields, so an untouched form does not open covered in refusals.
  showFieldErrors: boolean;
  announce: (message: string, kind?: StatusKind) => void;
  persist: () => void;
  clearSaved: () => boolean;
  readSaved: () => SynthesisStoredState | null;
  offerRecovery: (restore: (state: SynthesisStoredState) => Promise<void>) => void;
  rememberCandidates: (entries: SynthesisCandidate[]) => void;
  renderCandidatePanels: () => void;
  renderFieldErrors: () => void;
  render: () => void;
  forgetContext: (target: HTMLElement) => void;
};

export const createSynthesisRuntime = (
  root: HTMLElement,
  mount: HTMLElement,
  deps: SynthesisDeps
): SynthesisRuntime => {
  const config = readSynthesisConfig(root);
  const model = createSynthesisModel(config);
  const view = createSynthesisView(mount, config);
  const storage =
    deps.storage === undefined ? safeSynthesisStorage(root.ownerDocument.defaultView) : deps.storage;
  const draftID = deps.draftID || crypto.randomUUID();
  const storageKey = synthesisStorageKey(config.vaultID, config.anchor.key, draftID);
  const resumed = synthesisResume(storage, config.vaultID, config.anchor.key);
  const statusEl = queryElement(root, "[data-synthesize-status]", HTMLElement);
  const fallback = queryElement(root, "[data-synthesize-fallback]", HTMLElement);
  if (fallback) {
    fallback.hidden = true;
  }
  let storageWarned = false;
  const runtime: SynthesisRuntime = {
    config,
    model,
    view,
    fetcher: deps.fetch || ((input, init) => fetch(input, init)),
    clipboard:
      deps.clipboard === undefined
        ? (typeof navigator !== "undefined"
            ? (navigator.clipboard as WritableClipboard | undefined)
            : undefined) || null
        : deps.clipboard,
    confirmer: deps.confirm || ((message: string) => confirmAction({
      title: message === DISCLOSURE ? "Send context to Ask AI?" : "Change saved work?",
      description: message,
      confirmLabel: message === DISCLOSURE ? "Send context" : "Confirm",
      danger: message === DISCARD_CONFIRMATION,
    }, root.ownerDocument, message === DISCARD_CONFIRMATION ? view.discardButton : view.generateButton)),
    redirect: deps.redirect || ((path: string): void => window.location.assign(path)),
    candidates: new Map<string, SynthesisCandidate>(),
    suggestionEntries: { balanced: null, rediscover: null, connect: null },
    searchEntries: null,
    generating: false,
    previewing: false,
    applying: false,
    showFieldErrors: false,

    announce: (message, kind = ""): void => {
      setStatusText(statusEl, message, kind);
    },

    persist: (): void => {
      if (writeSynthesisState(storage, storageKey, synthesisStateFromModel(model))) {
        rememberSynthesisSession(draftID);
        return;
      }
      if (storage === null || storageWarned) {
        return;
      }
      storageWarned = true;
      setSynthesisWarning(
        view,
        "This browser refused to save your work, so leaving the page will lose the current selection and draft."
      );
    },

    // An absent storage never held anything, so only a refused removeItem leaves
    // saved work behind for a later visit to restore.
    clearSaved: (): boolean => clearSynthesisState(storage, storageKey) || storage === null,

    readSaved: (): SynthesisStoredState | null => readSynthesisState(storage, storageKey) || resumed,
    offerRecovery: (restore): void => mountSynthesisRecovery({
      root: mount, storage,
      keys: synthesisRecoveryKeys(storage, config.vaultID, config.anchor.key, storageKey),
      restore, confirm: runtime.confirmer,
    }),

    rememberCandidates: (entries): void => {
      for (const entry of entries) {
        runtime.candidates.set(entry.key, entry);
      }
    },

    renderCandidatePanels: () => renderCandidatePanels(runtime),

    renderFieldErrors: () => renderFieldErrors(runtime),

    render: () => render(runtime),

    // A context that the server no longer recognizes cannot produce a candidate
    // or a receipt, but it says nothing about the text the user wrote, so only
    // the server-side artifacts are dropped.
    forgetContext: (target): void => {
      invalidateSynthesisContext(model);
      model.contextStale = true;
      runtime.render();
      setStatusText(target, CONTEXT_GONE, "is-error");
    },
  };

  return runtime;
};

const renderCandidatePanels = (runtime: SynthesisRuntime): void => {
  const { model, view, config } = runtime;
  const selected = synthesisSelectedKeys(model);
  for (const definition of relatedModeDefinitions) {
    const entries = runtime.suggestionEntries[definition.mode];
    if (entries === null) {
      continue;
    }
    renderSynthesisCandidates(
      view.suggestionResults[definition.mode],
      entries,
      selected,
      config.anchor.key,
      false,
      SUGGESTIONS_EMPTY
    );
  }
  if (runtime.searchEntries !== null) {
    renderSynthesisCandidates(
      view.searchResults,
      runtime.searchEntries,
      selected,
      config.anchor.key,
      true,
      SEARCH_EMPTY
    );
  }
};

const renderFieldErrors = (runtime: SynthesisRuntime): void => {
  const { model, view } = runtime;
  const show = runtime.showFieldErrors;
  setSynthesisFieldError(view.titleError, show ? synthesisTitleError(model.title) : "");
  setSynthesisFieldError(view.folderError, show ? synthesisFolderError(model.folder) : "");
  setSynthesisFieldError(view.draftError, show ? synthesisDraftError(model.draft) : "");
};

const synthesisNextStep = (model: SynthesisModel): string => {
  if (model.context === null) {
    return model.contextStale
      ? "Rebuild context to include your updated sources or research options. Your draft is unchanged."
      : "Prepare context next. Add supporting notes only if they help; the anchor works on its own.";
  }
  if (model.title.trim() === "" || model.draft.trim() === "") {
    return "Next: give the note a title and write the draft.";
  }
  if (model.preview === null) {
    return "Next: preview the exact note before creating it.";
  }
  return "Next: review the preview, then create the note.";
};

const render = (runtime: SynthesisRuntime): void => {
  const { model, view, config } = runtime;
  view.nextStep.textContent = synthesisNextStep(model);
  renderSynthesisSources(view, model, config);
  renderSynthesisContext(view, model);
  renderSynthesisCandidate(view, model);
  renderSynthesisDestination(view, model);
  renderSynthesisPreview(view, model);
  runtime.renderFieldErrors();
  view.copyButton.disabled = model.context === null || !hasWritableClipboard(runtime.clipboard);
  view.candidateCopyButton.disabled = !hasWritableClipboard(runtime.clipboard);
  view.generateButton.disabled = model.context === null || runtime.generating;
  view.previewButton.disabled = runtime.previewing || runtime.applying;
  view.applyButton.disabled = model.preview === null || runtime.applying;
  view.discardButton.hidden = !synthesisHasSavedWork(model, config);
  view.prepareButton.classList.toggle("bn-primary", model.context === null);
  view.previewButton.classList.toggle("bn-primary", model.context !== null && model.preview === null);
  view.applyButton.classList.toggle("bn-primary", model.preview !== null);
  view.createHint.textContent = model.preview === null
    ? "Create note becomes available after a successful preview."
    : "The note will use the content and destination shown in the preview.";
};
