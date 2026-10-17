import { announceConfirmedWrite } from "./write-notice";
import { requestSynthesisApply, requestSynthesisPreview } from "./api";
import { setStatusText } from "./status";
import { invalidateSynthesisPreview, synthesisDraftError, synthesisFolderError, synthesisPreviewSignature, synthesisTitleError } from "./synthesize-model";
import { setSynthesisWarning } from "./synthesize-render";
import { errorCode, errorMessage, PREVIEW_SUPERSEDED, SAVED_STATE_STUCK, type SynthesisRuntime } from "./synthesize-runtime";

// The server validates all of this again; checking here only puts the first
// refusal beside the field that caused it.
const focusInvalidField = (runtime: SynthesisRuntime): boolean => {
  const { model, view } = runtime;
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

export const previewSynthesis = async (runtime: SynthesisRuntime): Promise<void> => {
  const { config, model, view } = runtime;
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
  if (focusInvalidField(runtime)) {
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

export const applySynthesis = async (runtime: SynthesisRuntime): Promise<void> => {
  const { config, model, view } = runtime;
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
    announceConfirmedWrite();
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
      errorCode(error) === "unconfirmed"
        ? errorMessage(error, "Couldn't confirm creation. Check History before retrying.")
        : `${errorMessage(error, "The note could not be created.")} Preview again to retry; nothing you wrote was lost.`,
      "is-error"
    );
  } finally {
    runtime.applying = false;
    runtime.render();
  }
};

