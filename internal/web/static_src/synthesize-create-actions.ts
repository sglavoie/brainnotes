import { requestSynthesisApply, requestSynthesisPreview } from "./api";
import { setStatusText } from "./status";
import {
  invalidateSynthesisPreview,
  synthesisDraftError,
  synthesisFolderError,
  synthesisPreviewSignature,
  synthesisTitleError,
  type SynthesisSource,
} from "./synthesize-model";
import { setSynthesisWarning } from "./synthesize-render";
import { resolveSynthesisSource } from "./synthesize-requests";
import { dedupeSynthesisSources } from "./synthesize-sources";
import {
  DISCARD_CONFIRMATION,
  errorCode,
  errorMessage,
  PREVIEW_SUPERSEDED,
  SAVED_STATE_STUCK,
  type SynthesisRuntime,
} from "./synthesize-runtime";

export type SynthesisCreateActions = {
  preview: () => Promise<void>;
  apply: () => Promise<void>;
  discard: () => void;
  restore: () => Promise<void>;
};

export const createSynthesisCreateActions = (runtime: SynthesisRuntime): SynthesisCreateActions => {
  const { config, model, view } = runtime;

  // The server validates all of this again; checking here only puts the first
  // refusal beside the field that caused it.
  const focusInvalidField = (): boolean => {
    runtime.showFieldErrors = true;
    runtime.renderFieldErrors();
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
    if (runtime.previewing || runtime.applying) {
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
    runtime.previewing = true;
    runtime.render();
    view.previewPanel.setAttribute("aria-busy", "true");
    setStatusText(view.previewStatus, "Building the note preview…", "is-loading");
    const requested = synthesisPreviewSignature(model);
    const superseded = (): boolean => synthesisPreviewSignature(model) !== requested;
    try {
      const response = await requestSynthesisPreview(runtime.fetcher, config.previewEndpoint, {
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
      runtime.render();
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
        runtime.forgetContext(view.previewStatus);
        return;
      }
      runtime.render();
      setStatusText(
        view.previewStatus,
        errorMessage(error, "The note preview could not be built."),
        "is-error"
      );
    } finally {
      runtime.previewing = false;
      view.previewPanel.setAttribute("aria-busy", "false");
      runtime.render();
    }
  };

  const apply = async (): Promise<void> => {
    const receipt = model.preview;
    if (receipt === null) {
      setStatusText(view.previewStatus, "Preview the note before creating it.", "is-error");
      return;
    }
    if (runtime.applying) {
      return;
    }
    runtime.applying = true;
    runtime.render();
    setStatusText(view.previewStatus, `Creating ${receipt.destination}…`, "is-loading");
    try {
      const response = await requestSynthesisApply(runtime.fetcher, config.applyEndpoint, {
        preview_id: receipt.preview_id,
        token: config.csrfToken,
      });
      const created = response.rel_path || receipt.destination;
      // Redirecting past a failed deletion would hide the only chance to say the
      // saved work is still there, so the navigation waits behind the warning.
      if (!runtime.clearSaved()) {
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
      runtime.redirect(response.path);
    } catch (error) {
      // A receipt is consumed before anything is written, so a failed apply has
      // always spent it. Preserve everything the user typed and ask for a fresh
      // preview rather than leaving a receipt that can only fail again.
      invalidateSynthesisPreview(model);
      runtime.render();
      setStatusText(
        view.previewStatus,
        `${errorMessage(error, "The note could not be created.")} Preview again to retry; nothing you wrote was lost.`,
        "is-error"
      );
    } finally {
      runtime.applying = false;
      runtime.render();
    }
  };

  const hasMeaningfulWork = (): boolean =>
    model.sources.length > 0 ||
    model.title.trim() !== "" ||
    model.prompt.trim() !== "" ||
    model.draft.trim() !== "" ||
    model.candidate !== null;

  const discard = (): void => {
    if (hasMeaningfulWork() && !runtime.confirmer(DISCARD_CONFIRMATION)) {
      runtime.announce("Kept the saved selection and draft.", "");
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
    runtime.showFieldErrors = false;
    view.titleInput.value = "";
    view.folderInput.value = model.folder;
    view.purposeSelect.value = model.purpose;
    view.promptInput.value = "";
    view.draftInput.value = "";
    const cleared = runtime.clearSaved();
    setSynthesisWarning(view, cleared ? "" : SAVED_STATE_STUCK);
    runtime.render();
    runtime.renderCandidatePanels();
    setStatusText(view.contextStatus, "", "");
    setStatusText(view.generateStatus, "", "");
    setStatusText(view.previewStatus, "", "");
    runtime.announce(
      cleared
        ? "Discarded the saved selection and draft."
        : "Cleared this page, but the saved copy could not be deleted.",
      cleared ? "is-success" : "is-error"
    );
  };

  const restore = async (): Promise<void> => {
    const stored = runtime.readSaved();
    if (!stored) {
      return;
    }
    model.title = stored.title;
    model.folder = stored.folder;
    model.purpose = config.purposes.includes(stored.purpose)
      ? stored.purpose
      : config.defaultPurpose;
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
      const source = await resolveSynthesisSource(runtime.fetcher, config.relatedEndpoint, key);
      if (source) {
        resolved.push(source);
      } else {
        missing.push(key);
      }
    }
    model.sources = dedupeSynthesisSources(resolved, config.anchor.key, config.maxSupporters);
    runtime.render();
    runtime.renderCandidatePanels();
    if (missing.length > 0) {
      setSynthesisWarning(
        view,
        `${missing.length} saved ${missing.length === 1 ? "note is" : "notes are"} no longer in this vault and ${missing.length === 1 ? "was" : "were"} removed from the selection: ${missing.join(", ")}.`
      );
    }
    runtime.persist();
  };

  return { preview, apply, discard, restore };
};
