import {
  requestSynthesisApply,
  requestSynthesisContext,
  requestSynthesisGenerate,
  requestSynthesisPreview,
  SynthesisRequestError,
} from "./api";
import { queryElement } from "./dom";
import { hasWritableClipboard } from "./clipboard";
import { relatedModeDefinitions } from "./related-model";
import { setStatusText, type StatusKind } from "./status";
import {
  createSynthesisModel,
  invalidateSynthesisContext,
  invalidateSynthesisPreview,
  readSynthesisConfig,
  synthesisContextSignature,
  synthesisDraftError,
  synthesisFolderError,
  synthesisPreviewSignature,
  synthesisSelectedKeys,
  synthesisSourcePosition,
  synthesisTitleError,
  type SynthesisConfig,
  type SynthesisModel,
} from "./synthesize-model";
import {
  createSynthesisView,
  renderSynthesisCandidate,
  renderSynthesisCandidates,
  renderSynthesisContext,
  renderSynthesisDestination,
  renderSynthesisPreview,
  renderSynthesisSources,
  selectSynthesisSuggestionMode,
  setSynthesisFieldError,
  setSynthesisWarning,
  type SynthesisView,
} from "./synthesize-render";
import {
  requestSynthesisSearch,
  requestSynthesisSuggestions,
  resolveSynthesisSource,
  type SynthesisFetch,
} from "./synthesize-requests";
import {
  addSynthesisSource,
  candidateFromRelatedResult,
  candidateFromSearchResult,
  dedupeSynthesisSources,
  moveSynthesisSource,
  removeSynthesisSource,
  reorderSynthesisSources,
  type SynthesisCandidate,
} from "./synthesize-sources";
import {
  clearSynthesisState,
  readSynthesisState,
  safeSynthesisStorage,
  synthesisStateFromModel,
  synthesisStorageKey,
  writeSynthesisState,
  type SynthesisStorageLike,
} from "./synthesize-storage";
import type { RelatedMode } from "./related-types";
import type { SynthesisSource } from "./synthesize-model";

type WritableClipboard = Pick<Clipboard, "writeText">;

export type SynthesisDeps = {
  fetch?: SynthesisFetch;
  storage?: SynthesisStorageLike | null;
  clipboard?: WritableClipboard | null;
  confirm?: (message: string) => boolean;
  redirect?: (path: string) => void;
};

export type SynthesisController = {
  ready: Promise<void>;
};

const errorMessage = (error: unknown, fallback: string): string =>
  error instanceof Error && error.message !== "" ? error.message : fallback;

const errorCode = (error: unknown): string =>
  error instanceof SynthesisRequestError ? error.code : "";

const modeLabel = (mode: RelatedMode): string =>
  relatedModeDefinitions.find((definition) => definition.mode === mode)?.label || mode;

const SUGGESTIONS_EMPTY = "No suggestions are available in this mode.";
const SEARCH_EMPTY = "No notes matched that search.";
const CONTEXT_GONE =
  "The context packet has expired or is no longer valid. Rebuild it, then preview again. Your draft is unchanged.";
const CONTEXT_SUPERSEDED =
  "The sources, purpose, or prompt changed while that packet was building, so it was discarded. Prepare the context again.";
const PREVIEW_SUPERSEDED =
  "The note changed while that preview was building, so it was discarded. Preview again.";
const DISCLOSURE = [
  "Generate sends the prepared context packet to your configured [agent].command.",
  "The packet contains the anchor note's complete body, exact excerpts from every selected source, and your prompt.",
  "Your prompt and that packet are retained in server memory until the context expires, is evicted to keep memory bounded, or the server restarts, and are never written to disk.",
  "The stored packet is sent to the command only after you accept this notice. The generated candidate is not stored on the server and never enters your draft on its own. Continue?",
].join("\n");
const DISCARD_CONFIRMATION =
  "Discard the saved sources, draft, and generated candidate for this note? This cannot be undone.";
const SAVED_STATE_STUCK =
  "This browser refused to delete the saved sources and draft for this note, so they will come back the next time you open the workbench.";

export const createSynthesisController = (
  root: HTMLElement,
  mount: HTMLElement,
  deps: SynthesisDeps = {}
): SynthesisController => {
  const config = readSynthesisConfig(root);
  const model = createSynthesisModel(config);
  const view = createSynthesisView(mount, config);
  const fetcher: SynthesisFetch = deps.fetch || ((input, init) => fetch(input, init));
  const storage =
    deps.storage === undefined ? safeSynthesisStorage(root.ownerDocument.defaultView) : deps.storage;
  const clipboard =
    deps.clipboard === undefined
      ? (typeof navigator !== "undefined" ? (navigator.clipboard as WritableClipboard | undefined) : undefined) ||
        null
      : deps.clipboard;
  const confirmer = deps.confirm || ((message: string): boolean => window.confirm(message));
  const redirect = deps.redirect || ((path: string): void => window.location.assign(path));
  const storageKey = synthesisStorageKey(config.vaultID, config.anchor.key);
  const statusEl = queryElement(root, "[data-synthesize-status]", HTMLElement);
  const fallback = queryElement(root, "[data-synthesize-fallback]", HTMLElement);
  if (fallback) {
    fallback.hidden = true;
  }

  const candidates = new Map<string, SynthesisCandidate>();
  const suggestionRequests: Record<RelatedMode, number> = {
    balanced: 0,
    rediscover: 0,
    connect: 0,
  };
  const suggestionEntries: Record<RelatedMode, SynthesisCandidate[] | null> = {
    balanced: null,
    rediscover: null,
    connect: null,
  };
  let searchEntries: SynthesisCandidate[] | null = null;
  let activeMode: RelatedMode = "balanced";
  let searchRequest = 0;
  let storageWarned = false;
  let disclosureAccepted = false;
  let generating = false;
  let previewing = false;
  let applying = false;
  // Validation messages stay hidden until the user asks for something that needs
  // valid fields, so an untouched form does not open covered in refusals.
  let showFieldErrors = false;

  const buttonByAttribute = (attribute: string, value: string): HTMLButtonElement | null => {
    for (const button of view.sourceList.querySelectorAll<HTMLButtonElement>(`[${attribute}]`)) {
      if (button.getAttribute(attribute) === value) {
        return button;
      }
    }
    return null;
  };

  const announce = (message: string, kind: StatusKind = ""): void => {
    setStatusText(statusEl, message, kind);
  };

  const persist = (): void => {
    if (writeSynthesisState(storage, storageKey, synthesisStateFromModel(model))) {
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
  };

  // An absent storage never held anything, so only a refused removeItem leaves
  // saved work behind for a later visit to restore.
  const clearSaved = (): boolean => clearSynthesisState(storage, storageKey) || storage === null;

  const rememberCandidates = (entries: SynthesisCandidate[]): void => {
    for (const entry of entries) {
      candidates.set(entry.key, entry);
    }
  };

  const renderCandidatePanels = (): void => {
    const selected = synthesisSelectedKeys(model);
    for (const definition of relatedModeDefinitions) {
      const entries = suggestionEntries[definition.mode];
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
    if (searchEntries !== null) {
      renderSynthesisCandidates(
        view.searchResults,
        searchEntries,
        selected,
        config.anchor.key,
        true,
        SEARCH_EMPTY
      );
    }
  };

  const renderFieldErrors = (): void => {
    setSynthesisFieldError(view.titleError, showFieldErrors ? synthesisTitleError(model.title) : "");
    setSynthesisFieldError(
      view.folderError,
      showFieldErrors ? synthesisFolderError(model.folder) : ""
    );
    setSynthesisFieldError(view.draftError, showFieldErrors ? synthesisDraftError(model.draft) : "");
  };

  const render = (): void => {
    renderSynthesisSources(view, model, config);
    renderSynthesisContext(view, model);
    renderSynthesisCandidate(view, model);
    renderSynthesisDestination(view, model);
    renderSynthesisPreview(view, model);
    renderFieldErrors();
    view.copyButton.disabled = model.context === null || !hasWritableClipboard(clipboard);
    view.candidateCopyButton.disabled = !hasWritableClipboard(clipboard);
    view.generateButton.disabled = model.context === null || generating;
    view.previewButton.disabled = previewing || applying;
    view.applyButton.disabled = model.preview === null || applying;
  };

  const focusHandle = (key: string): void => {
    (buttonByAttribute("data-synthesize-handle", key) || view.selectionStatus).focus({
      preventScroll: true,
    });
  };

  // A removed row takes its own controls with it, so focus lands on whatever
  // now occupies that position, then the row before it, then the selection
  // summary — never nothing.
  const focusRemoveAt = (index: number): void => {
    const source = model.sources[index] || model.sources[index - 1];
    const button = source ? buttonByAttribute("data-synthesize-remove", source.key) : null;
    (button || view.selectionStatus).focus({ preventScroll: true });
  };

  const addSource = (key: string): void => {
    const candidate = candidates.get(key);
    if (!candidate) {
      announce("That note is no longer available to add.", "is-error");
      return;
    }
    const outcome = addSynthesisSource(model, config, candidate);
    announce(outcome.message, outcome.ok ? "is-success" : "is-error");
    if (!outcome.ok) {
      return;
    }
    render();
    renderCandidatePanels();
    persist();
  };

  const removeSource = (key: string): void => {
    const index = model.sources.findIndex((source) => source.key === key);
    const outcome = removeSynthesisSource(model, key);
    announce(outcome.message, outcome.ok ? "is-success" : "is-error");
    if (!outcome.ok) {
      return;
    }
    render();
    renderCandidatePanels();
    persist();
    focusRemoveAt(index);
  };

  const moveSource = (key: string, direction: -1 | 1): void => {
    const outcome = moveSynthesisSource(model, key, direction);
    announce(outcome.message, outcome.ok ? "is-success" : "is-error");
    if (!outcome.ok) {
      return;
    }
    render();
    persist();
    focusHandle(key);
  };

  const applyDragOrder = (key: string): void => {
    const keys = Array.from(
      view.sourceList.querySelectorAll<HTMLElement>("[data-synthesize-source]")
    )
      .filter((row) => !row.hasAttribute("data-synthesize-anchor"))
      .map((row) => row.getAttribute("data-synthesize-source") || "");
    const moved = model.sources.find((source) => source.key === key);
    const outcome = reorderSynthesisSources(model, keys);
    render();
    if (outcome.ok) {
      persist();
      announce(
        `Moved ${moved?.title || key} to position ${synthesisSourcePosition(model, key)} of ${model.sources.length + 1}.`,
        "is-success"
      );
    }
    focusHandle(key);
  };

  const supporterRows = (): HTMLElement[] =>
    Array.from(view.sourceList.querySelectorAll<HTMLElement>("[data-synthesize-source]")).filter(
      (row) => !row.hasAttribute("data-synthesize-anchor")
    );

  const startDrag = (row: HTMLElement, event: PointerEvent): void => {
    if (event.button !== 0) {
      return;
    }
    event.preventDefault();
    const key = row.getAttribute("data-synthesize-source") || "";
    const win = view.sourceList.ownerDocument.defaultView;
    if (!win) {
      return;
    }
    row.classList.add("is-dragging");

    const onPointerMove = (moveEvent: PointerEvent): void => {
      const next = supporterRows().find((other) => {
        if (other === row) {
          return false;
        }
        const rect = other.getBoundingClientRect();
        return moveEvent.clientY < rect.top + rect.height / 2;
      });
      if (next) {
        view.sourceList.insertBefore(row, next);
      } else {
        view.sourceList.appendChild(row);
      }
    };

    const finishDrag = (): void => {
      row.classList.remove("is-dragging");
      win.removeEventListener("pointermove", onPointerMove);
      win.removeEventListener("pointerup", finishDrag);
      win.removeEventListener("pointercancel", finishDrag);
      applyDragOrder(key);
    };

    win.addEventListener("pointermove", onPointerMove);
    win.addEventListener("pointerup", finishDrag, { once: true });
    win.addEventListener("pointercancel", finishDrag, { once: true });
  };

  const showSuggestionCount = (mode: RelatedMode): void => {
    const shown = view.suggestionResults[mode].querySelectorAll("[data-synthesize-candidate]").length;
    setStatusText(
      view.suggestionStatus,
      shown === 0
        ? SUGGESTIONS_EMPTY
        : `${shown} ${shown === 1 ? "suggestion" : "suggestions"} in ${modeLabel(mode)}.`,
      shown === 0 ? "is-empty" : "is-success"
    );
  };

  const loadSuggestions = async (mode: RelatedMode): Promise<void> => {
    const requestID = suggestionRequests[mode] + 1;
    suggestionRequests[mode] = requestID;
    view.suggestionPanels[mode].setAttribute("aria-busy", "true");
    if (mode === activeMode) {
      setStatusText(view.suggestionStatus, `Loading ${modeLabel(mode)} suggestions…`, "is-loading");
    }
    try {
      const response = await requestSynthesisSuggestions(
        fetcher,
        config.relatedEndpoint,
        config.anchor.key,
        mode
      );
      if (suggestionRequests[mode] !== requestID) {
        return;
      }
      const entries = response.results.map(candidateFromRelatedResult);
      rememberCandidates(entries);
      suggestionEntries[mode] = entries;
      renderSynthesisCandidates(
        view.suggestionResults[mode],
        entries,
        synthesisSelectedKeys(model),
        config.anchor.key,
        false,
        SUGGESTIONS_EMPTY
      );
      if (mode === activeMode) {
        showSuggestionCount(mode);
      }
    } catch (error) {
      if (suggestionRequests[mode] !== requestID) {
        return;
      }
      if (mode === activeMode) {
        setStatusText(
          view.suggestionStatus,
          errorMessage(error, "Suggestions could not be loaded."),
          "is-error"
        );
      }
    } finally {
      if (suggestionRequests[mode] === requestID) {
        view.suggestionPanels[mode].setAttribute("aria-busy", "false");
      }
    }
  };

  const selectMode = (mode: RelatedMode): Promise<void> => {
    activeMode = mode;
    selectSynthesisSuggestionMode(view, mode);
    if (suggestionEntries[mode] === null) {
      return loadSuggestions(mode);
    }
    showSuggestionCount(mode);
    return Promise.resolve();
  };

  const runSearch = async (query: string): Promise<void> => {
    const trimmed = query.trim();
    if (trimmed === "") {
      searchRequest += 1;
      searchEntries = null;
      view.searchResults.replaceChildren();
      setStatusText(view.searchStatus, "Enter a search term to find notes.", "");
      return;
    }
    const requestID = searchRequest + 1;
    searchRequest = requestID;
    setStatusText(view.searchStatus, `Searching for ${trimmed}…`, "is-loading");
    try {
      const results = await requestSynthesisSearch(fetcher, config.searchEndpoint, trimmed);
      if (searchRequest !== requestID) {
        return;
      }
      const entries = results.flatMap((result) => {
        const candidate = candidateFromSearchResult(result);
        return candidate ? [candidate] : [];
      });
      rememberCandidates(entries);
      searchEntries = entries;
      renderSynthesisCandidates(
        view.searchResults,
        entries,
        synthesisSelectedKeys(model),
        config.anchor.key,
        true,
        SEARCH_EMPTY
      );
      const shown = view.searchResults.querySelectorAll("[data-synthesize-candidate]").length;
      setStatusText(
        view.searchStatus,
        shown === 0 ? SEARCH_EMPTY : `${shown} ${shown === 1 ? "match" : "matches"} for ${trimmed}.`,
        shown === 0 ? "is-empty" : "is-success"
      );
    } catch (error) {
      if (searchRequest !== requestID) {
        return;
      }
      searchEntries = null;
      view.searchResults.replaceChildren();
      setStatusText(view.searchStatus, errorMessage(error, "Search failed."), "is-error");
    }
  };

  const prepareContext = async (): Promise<void> => {
    if (config.csrfToken === "") {
      setStatusText(view.contextStatus, "Context preparation is unavailable.", "is-error");
      return;
    }
    view.prepareButton.disabled = true;
    setStatusText(view.contextStatus, "Building context packet…", "is-loading");
    const requested = synthesisContextSignature(model);
    const superseded = (): boolean => synthesisContextSignature(model) !== requested;
    try {
      const response = await requestSynthesisContext(fetcher, config.contextEndpoint, {
        source: config.anchor.key,
        candidates: synthesisSelectedKeys(model),
        purpose: model.purpose,
        prompt: model.prompt,
        token: config.csrfToken,
      });
      if (superseded()) {
        setStatusText(view.contextStatus, CONTEXT_SUPERSEDED, "");
        return;
      }
      invalidateSynthesisPreview(model);
      model.context = response;
      model.contextStale = false;
      render();
      setStatusText(
        view.contextStatus,
        `Context ready. It expires at ${response.expires_at || "the server's timeout"}.`,
        "is-success"
      );
    } catch (error) {
      if (superseded()) {
        setStatusText(view.contextStatus, CONTEXT_SUPERSEDED, "");
        return;
      }
      invalidateSynthesisContext(model);
      model.contextStale = true;
      render();
      setStatusText(
        view.contextStatus,
        errorMessage(error, "The context packet could not be built."),
        "is-error"
      );
    } finally {
      view.prepareButton.disabled = false;
    }
  };

  const copyContext = async (): Promise<void> => {
    const context = model.context;
    if (!context) {
      setStatusText(view.contextStatus, "Prepare a context packet first.", "is-error");
      return;
    }
    if (!hasWritableClipboard(clipboard)) {
      setStatusText(
        view.contextStatus,
        "Clipboard unavailable. Select the packet Markdown below and copy it manually.",
        "is-error"
      );
      return;
    }
    view.copyButton.disabled = true;
    try {
      await clipboard.writeText(context.markdown);
      setStatusText(view.contextStatus, "Context packet copied.", "is-success");
    } catch {
      setStatusText(
        view.contextStatus,
        "Copy failed. Select the packet Markdown below and copy it manually.",
        "is-error"
      );
    } finally {
      view.copyButton.disabled = false;
    }
  };

  // A context that the server no longer recognizes cannot produce a candidate or
  // a receipt, but it says nothing about the text the user wrote, so only the
  // server-side artifacts are dropped.
  const forgetContext = (target: HTMLElement): void => {
    invalidateSynthesisContext(model);
    model.contextStale = true;
    render();
    setStatusText(target, CONTEXT_GONE, "is-error");
  };

  const generate = async (): Promise<void> => {
    if (model.context === null) {
      setStatusText(view.generateStatus, "Prepare a context packet first.", "is-error");
      return;
    }
    if (generating) {
      return;
    }
    if (config.agentEnabled && !disclosureAccepted) {
      disclosureAccepted = confirmer(DISCLOSURE);
      if (!disclosureAccepted) {
        setStatusText(view.generateStatus, "Generation canceled.", "");
        return;
      }
    }
    generating = true;
    view.generateButton.disabled = true;
    view.candidatePanel.setAttribute("aria-busy", "true");
    setStatusText(view.generateStatus, "Generating a candidate draft…", "is-loading");
    try {
      const response = await requestSynthesisGenerate(fetcher, config.generateEndpoint, {
        context_id: model.context.context_id,
        token: config.csrfToken,
      });
      if (!response.enabled) {
        setStatusText(
          view.generateStatus,
          response.message || "Generation is unavailable until [agent].command is configured.",
          "is-empty"
        );
        return;
      }
      model.candidate = { markdown: response.output, html: response.rendered_html };
      render();
      persist();
      setStatusText(
        view.generateStatus,
        "Candidate ready. Your draft is unchanged until you replace or append it.",
        "is-success"
      );
      view.candidatePanel.focus({ preventScroll: true });
    } catch (error) {
      if (errorCode(error) === "not_found") {
        forgetContext(view.generateStatus);
        return;
      }
      setStatusText(
        view.generateStatus,
        errorMessage(error, "The generation command failed."),
        "is-error"
      );
    } finally {
      generating = false;
      view.candidatePanel.setAttribute("aria-busy", "false");
      view.generateButton.disabled = model.context === null;
    }
  };

  const useCandidate = (mode: "replace" | "append"): void => {
    const candidate = model.candidate;
    if (candidate === null) {
      setStatusText(view.generateStatus, "There is no candidate to use.", "is-error");
      return;
    }
    const existing = model.draft.replace(/\s+$/, "");
    model.draft =
      mode === "replace" || existing === "" ? candidate.markdown : `${existing}\n\n${candidate.markdown}`;
    view.draftInput.value = model.draft;
    invalidateSynthesisPreview(model);
    render();
    persist();
    setStatusText(
      view.generateStatus,
      mode === "replace" ? "Draft replaced with the candidate." : "Candidate appended to the draft.",
      "is-success"
    );
    view.draftInput.focus({ preventScroll: true });
  };

  const copyCandidate = async (): Promise<void> => {
    const candidate = model.candidate;
    if (candidate === null) {
      setStatusText(view.generateStatus, "There is no candidate to copy.", "is-error");
      return;
    }
    if (!hasWritableClipboard(clipboard)) {
      setStatusText(
        view.generateStatus,
        "Clipboard unavailable. Select the candidate Markdown above and copy it manually.",
        "is-error"
      );
      return;
    }
    view.candidateCopyButton.disabled = true;
    try {
      await clipboard.writeText(candidate.markdown);
      setStatusText(view.generateStatus, "Candidate copied.", "is-success");
    } catch {
      setStatusText(
        view.generateStatus,
        "Copy failed. Select the candidate Markdown above and copy it manually.",
        "is-error"
      );
    } finally {
      view.candidateCopyButton.disabled = !hasWritableClipboard(clipboard);
    }
  };

  const dismissCandidate = (): void => {
    if (model.candidate === null) {
      return;
    }
    model.candidate = null;
    render();
    persist();
    setStatusText(view.generateStatus, "Candidate dismissed. Your draft is unchanged.", "");
    view.generateButton.focus({ preventScroll: true });
  };

  // The server validates all of this again; checking here only puts the first
  // refusal beside the field that caused it.
  const focusInvalidField = (): boolean => {
    showFieldErrors = true;
    renderFieldErrors();
    const fields: [string, HTMLElement][] = [
      [synthesisTitleError(model.title), view.titleInput],
      [synthesisFolderError(model.folder), view.folderInput],
      [synthesisDraftError(model.draft), view.draftInput],
    ];
    const invalid = fields.find(([message]) => message !== "");
    if (!invalid) {
      return false;
    }
    invalid[1].focus({ preventScroll: true });
    setStatusText(view.previewStatus, invalid[0], "is-error");
    return true;
  };

  const preview = async (): Promise<void> => {
    if (previewing || applying) {
      return;
    }
    if (model.context === null) {
      setStatusText(
        view.previewStatus,
        "Prepare a context packet before previewing the note.",
        "is-error"
      );
      return;
    }
    if (focusInvalidField()) {
      return;
    }
    previewing = true;
    render();
    view.previewPanel.setAttribute("aria-busy", "true");
    setStatusText(view.previewStatus, "Building the note preview…", "is-loading");
    const requested = synthesisPreviewSignature(model);
    const superseded = (): boolean => synthesisPreviewSignature(model) !== requested;
    try {
      const response = await requestSynthesisPreview(fetcher, config.previewEndpoint, {
        context_id: model.context.context_id,
        title: model.title.trim(),
        folder: model.folder.trim(),
        draft: model.draft,
        token: config.csrfToken,
      });
      if (superseded()) {
        setStatusText(view.previewStatus, PREVIEW_SUPERSEDED, "");
        return;
      }
      model.preview = response;
      model.previewStale = false;
      render();
      setStatusText(
        view.previewStatus,
        `Preview ready for ${response.destination}. Review it, then create the note.`,
        "is-success"
      );
      view.previewPanel.focus({ preventScroll: true });
    } catch (error) {
      if (superseded()) {
        setStatusText(view.previewStatus, PREVIEW_SUPERSEDED, "");
        return;
      }
      invalidateSynthesisPreview(model);
      if (errorCode(error) === "not_found") {
        forgetContext(view.previewStatus);
        return;
      }
      render();
      setStatusText(
        view.previewStatus,
        errorMessage(error, "The note preview could not be built."),
        "is-error"
      );
    } finally {
      previewing = false;
      view.previewPanel.setAttribute("aria-busy", "false");
      render();
    }
  };

  const apply = async (): Promise<void> => {
    const receipt = model.preview;
    if (receipt === null) {
      setStatusText(view.previewStatus, "Preview the note before creating it.", "is-error");
      return;
    }
    if (applying) {
      return;
    }
    applying = true;
    render();
    setStatusText(view.previewStatus, `Creating ${receipt.destination}…`, "is-loading");
    try {
      const response = await requestSynthesisApply(fetcher, config.applyEndpoint, {
        preview_id: receipt.preview_id,
        token: config.csrfToken,
      });
      const created = response.rel_path || receipt.destination;
      // Redirecting past a failed deletion would hide the only chance to say the
      // saved work is still there, so the navigation waits behind the warning.
      if (!clearSaved()) {
        // The receipt is spent and the destination is now taken, so the button
        // that stays on screen must not offer a create that can only fail.
        invalidateSynthesisPreview(model);
        setSynthesisWarning(view, SAVED_STATE_STUCK, {
          href: response.path,
          label: `Open ${created}`,
        });
        setStatusText(
          view.previewStatus,
          `Created ${created}. The saved copy of your work could not be deleted, so the note was not opened automatically.`,
          "is-success"
        );
        return;
      }
      setStatusText(view.previewStatus, `Created ${created}. Opening the note…`, "is-success");
      redirect(response.path);
    } catch (error) {
      // A receipt is consumed before anything is written, so a failed apply has
      // always spent it. Preserve everything the user typed and ask for a fresh
      // preview rather than leaving a receipt that can only fail again.
      invalidateSynthesisPreview(model);
      render();
      setStatusText(
        view.previewStatus,
        `${errorMessage(error, "The note could not be created.")} Preview again to retry; nothing you wrote was lost.`,
        "is-error"
      );
    } finally {
      applying = false;
      render();
    }
  };

  const hasMeaningfulWork = (): boolean =>
    model.sources.length > 0 ||
    model.title.trim() !== "" ||
    model.prompt.trim() !== "" ||
    model.draft.trim() !== "" ||
    model.candidate !== null;

  const discard = (): void => {
    if (hasMeaningfulWork() && !confirmer(DISCARD_CONFIRMATION)) {
      announce("Kept the saved selection and draft.", "");
      return;
    }
    model.sources = [];
    model.title = "";
    model.folder = config.defaultFolder;
    model.purpose = config.defaultPurpose;
    model.prompt = "";
    model.draft = "";
    model.candidate = null;
    model.context = null;
    model.contextStale = false;
    model.preview = null;
    model.previewStale = false;
    showFieldErrors = false;
    view.titleInput.value = "";
    view.folderInput.value = model.folder;
    view.purposeSelect.value = model.purpose;
    view.promptInput.value = "";
    view.draftInput.value = "";
    const cleared = clearSaved();
    setSynthesisWarning(view, cleared ? "" : SAVED_STATE_STUCK);
    render();
    renderCandidatePanels();
    setStatusText(view.contextStatus, "", "");
    setStatusText(view.generateStatus, "", "");
    setStatusText(view.previewStatus, "", "");
    announce(
      cleared
        ? "Discarded the saved selection and draft."
        : "Cleared this page, but the saved copy could not be deleted.",
      cleared ? "is-success" : "is-error"
    );
  };

  const restore = async (): Promise<void> => {
    const stored = readSynthesisState(storage, storageKey);
    if (!stored) {
      return;
    }
    model.title = stored.title;
    model.folder = stored.folder;
    model.purpose = config.purposes.includes(stored.purpose) ? stored.purpose : config.defaultPurpose;
    model.prompt = stored.prompt;
    model.draft = stored.draft;
    model.candidate = stored.candidate === "" ? null : { markdown: stored.candidate, html: "" };
    view.titleInput.value = model.title;
    view.folderInput.value = model.folder;
    view.purposeSelect.value = model.purpose;
    view.promptInput.value = model.prompt;
    view.draftInput.value = model.draft;

    const resolved: SynthesisSource[] = [];
    const missing: string[] = [];
    for (const key of stored.sources) {
      const source = await resolveSynthesisSource(fetcher, config.relatedEndpoint, key);
      if (source) {
        resolved.push(source);
      } else {
        missing.push(key);
      }
    }
    model.sources = dedupeSynthesisSources(resolved, config.anchor.key, config.maxSupporters);
    render();
    renderCandidatePanels();
    if (missing.length > 0) {
      setSynthesisWarning(
        view,
        `${missing.length} saved ${missing.length === 1 ? "note is" : "notes are"} no longer in this vault and ${missing.length === 1 ? "was" : "were"} removed from the selection: ${missing.join(", ")}.`
      );
    }
    persist();
  };

  view.sourceList.addEventListener("click", (event) => {
    const target = event.target instanceof Element ? event.target : null;
    const remove = target?.closest<HTMLButtonElement>("[data-synthesize-remove]");
    if (remove) {
      removeSource(remove.getAttribute("data-synthesize-remove") || "");
    }
  });

  view.sourceList.addEventListener("keydown", (event) => {
    const target = event.target instanceof Element ? event.target : null;
    const handle = target?.closest<HTMLButtonElement>("[data-synthesize-handle]");
    if (!handle || (event.key !== "ArrowUp" && event.key !== "ArrowDown")) {
      return;
    }
    event.preventDefault();
    moveSource(
      handle.getAttribute("data-synthesize-handle") || "",
      event.key === "ArrowUp" ? -1 : 1
    );
  });

  view.sourceList.addEventListener("pointerdown", (event) => {
    const target = event.target instanceof Element ? event.target : null;
    const handle = target?.closest<HTMLElement>("[data-synthesize-handle]");
    const row = handle?.closest<HTMLElement>("[data-synthesize-source]");
    if (!row || row.hasAttribute("data-synthesize-anchor")) {
      return;
    }
    startDrag(row, event);
  });

  mount.addEventListener("click", (event) => {
    const target = event.target instanceof Element ? event.target : null;
    const add = target?.closest<HTMLButtonElement>("[data-synthesize-add]");
    if (!add || add.disabled) {
      return;
    }
    addSource(add.getAttribute("data-synthesize-add") || "");
  });

  for (const definition of relatedModeDefinitions) {
    const tab = view.suggestionTabs[definition.mode];
    tab.addEventListener("click", () => {
      void selectMode(definition.mode);
    });
    tab.addEventListener("keydown", (event) => {
      const index = relatedModeDefinitions.findIndex((item) => item.mode === definition.mode);
      let target = index;
      switch (event.key) {
        case "ArrowLeft":
          target = (index - 1 + relatedModeDefinitions.length) % relatedModeDefinitions.length;
          break;
        case "ArrowRight":
          target = (index + 1) % relatedModeDefinitions.length;
          break;
        default:
          return;
      }
      event.preventDefault();
      const next = relatedModeDefinitions[target].mode;
      view.suggestionTabs[next].focus();
      void selectMode(next);
    });
  }

  view.searchForm.addEventListener("submit", (event) => {
    event.preventDefault();
    void runSearch(view.searchInput.value);
  });

  // Title, folder, and draft never reach the context packet, so they invalidate
  // the receipt that names the exact bytes and leave the packet alone.
  view.titleInput.addEventListener("input", () => {
    model.title = view.titleInput.value;
    invalidateSynthesisPreview(model);
    render();
    persist();
  });
  view.folderInput.addEventListener("input", () => {
    model.folder = view.folderInput.value;
    invalidateSynthesisPreview(model);
    render();
    persist();
  });
  view.draftInput.addEventListener("input", () => {
    model.draft = view.draftInput.value;
    invalidateSynthesisPreview(model);
    render();
    persist();
  });
  view.purposeSelect.addEventListener("change", () => {
    model.purpose = view.purposeSelect.value;
    invalidateSynthesisContext(model);
    render();
    persist();
  });
  view.promptInput.addEventListener("input", () => {
    model.prompt = view.promptInput.value;
    invalidateSynthesisContext(model);
    render();
    persist();
  });

  view.prepareButton.addEventListener("click", () => {
    void prepareContext();
  });
  view.copyButton.addEventListener("click", () => {
    void copyContext();
  });
  view.discardButton.addEventListener("click", () => {
    discard();
  });
  view.generateButton.addEventListener("click", () => {
    void generate();
  });
  view.candidateReplaceButton.addEventListener("click", () => {
    useCandidate("replace");
  });
  view.candidateAppendButton.addEventListener("click", () => {
    useCandidate("append");
  });
  view.candidateCopyButton.addEventListener("click", () => {
    void copyCandidate();
  });
  view.candidateDismissButton.addEventListener("click", () => {
    dismissCandidate();
  });
  view.previewButton.addEventListener("click", () => {
    void preview();
  });
  view.applyButton.addEventListener("click", () => {
    void apply();
  });

  view.folderInput.value = model.folder;
  view.purposeSelect.value = model.purpose;
  render();

  const ready = (async (): Promise<void> => {
    await restore();
    await selectMode("balanced");
  })();
  return { ready };
};

export const initSynthesize = (deps: SynthesisDeps = {}): SynthesisController | null => {
  const root = queryElement(document, "[data-synthesize-root]", HTMLElement);
  if (!root) {
    return null;
  }
  const mount = queryElement(root, "[data-synthesize-workbench]", HTMLElement);
  if (!mount || root.getAttribute("data-synthesize-anchor-key") === "") {
    return null;
  }
  return createSynthesisController(root, mount, deps);
};
