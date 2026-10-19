import type { SynthesisStoredState } from "./synthesize-storage";
import { previewSynthesis, applySynthesis } from "./synthesize-write-actions";
import { setStatusText } from "./status";
import { synthesisHasSavedWork } from "./synthesize-model";
import { setSynthesisWarning } from "./synthesize-render";
import { createSynthesisSourceRestorer } from "./synthesize-restore";
import {
  DISCARD_CONFIRMATION,
  SAVED_STATE_STUCK,
  type SynthesisRuntime,
} from "./synthesize-runtime";

export type SynthesisCreateActions = {
  preview: () => Promise<void>;
  apply: () => Promise<void>;
  discard: () => Promise<void>;
  // Resolves true when every saved source settled, false when some could not
  // be reached and are kept in storage for a retry.
  restore: (saved?: SynthesisStoredState) => Promise<boolean>;
};

export const createSynthesisCreateActions = (runtime: SynthesisRuntime): SynthesisCreateActions => {
  const { config, model, view } = runtime;
  const restoreSources = createSynthesisSourceRestorer(runtime);

  let discarding = false;
  const discard = async (): Promise<void> => {
    if (discarding) return;
    discarding = true;
    try {
      if (synthesisHasSavedWork(model, config) && !await runtime.confirmer(DISCARD_CONFIRMATION)) {
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
      runtime.unresolvedSources = [];
      view.titleInput.value = "";
      view.folderInput.value = model.folder;
      view.purposeSelect.value = model.purpose;
      view.promptInput.value = "";
      view.draftInput.value = "";
      const cleared = runtime.clearSaved();
      runtime.savedStateStuck = !cleared;
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
    } finally { discarding = false; }
  };

  const restore = async (saved?: SynthesisStoredState): Promise<boolean> => {
    const stored = saved || runtime.readSaved();
    if (!stored) {
      return true;
    }
    model.context = null;
    model.preview = null;
    model.contextStale = false;
    model.previewStale = false;
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

    return restoreSources(stored.sources);
  };

  return { preview: () => previewSynthesis(runtime), apply: () => applySynthesis(runtime), discard, restore };
};
