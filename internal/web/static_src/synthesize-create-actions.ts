import type { SynthesisStoredState } from "./synthesize-storage";
import { previewSynthesis, applySynthesis } from "./synthesize-write-actions";
import { setStatusText } from "./status";
import type { SynthesisSource } from "./synthesize-model";
import { synthesisHasSavedWork } from "./synthesize-model";
import { setSynthesisWarning } from "./synthesize-render";
import { resolveSynthesisSource } from "./synthesize-requests";
import { dedupeSynthesisSources } from "./synthesize-sources";
import {
  DISCARD_CONFIRMATION,
  SAVED_STATE_STUCK,
  type SynthesisRuntime,
} from "./synthesize-runtime";

export type SynthesisCreateActions = {
  preview: () => Promise<void>;
  apply: () => Promise<void>;
  discard: () => Promise<void>;
  restore: (saved?: SynthesisStoredState) => Promise<void>;
};

export const createSynthesisCreateActions = (runtime: SynthesisRuntime): SynthesisCreateActions => {
  const { config, model, view } = runtime;

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
    } finally { discarding = false; }
  };

  const restore = async (saved?: SynthesisStoredState): Promise<void> => {
    const stored = saved || runtime.readSaved();
    if (!stored) {
      return;
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

  return { preview: () => previewSynthesis(runtime), apply: () => applySynthesis(runtime), discard, restore };
};
