import { requestSynthesisContext, requestSynthesisGenerate } from "./api";
import { hasWritableClipboard } from "./clipboard";
import { setStatusText } from "./status";
import {
  invalidateSynthesisContext,
  invalidateSynthesisPreview,
  synthesisContextSignature,
  synthesisSelectedKeys,
} from "./synthesize-model";
import {
  CONTEXT_SUPERSEDED,
  DISCLOSURE,
  errorCode,
  errorMessage,
  type SynthesisRuntime,
} from "./synthesize-runtime";

export type SynthesisContextActions = {
  prepareContext: () => Promise<void>;
  copyContext: () => Promise<void>;
  generate: () => Promise<void>;
  useCandidate: (mode: "replace" | "append") => void;
  copyCandidate: () => Promise<void>;
  dismissCandidate: () => void;
};

export const createSynthesisContextActions = (
  runtime: SynthesisRuntime
): SynthesisContextActions => {
  const { config, model, view } = runtime;
  let disclosureAccepted = false;

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
      const response = await requestSynthesisContext(runtime.fetcher, config.contextEndpoint, {
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
      runtime.render();
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
      runtime.render();
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
    if (!hasWritableClipboard(runtime.clipboard)) {
      setStatusText(
        view.contextStatus,
        "Clipboard unavailable. Select the packet Markdown below and copy it manually.",
        "is-error"
      );
      return;
    }
    view.copyButton.disabled = true;
    try {
      await runtime.clipboard.writeText(context.markdown);
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

  const generate = async (): Promise<void> => {
    if (model.context === null) {
      setStatusText(view.generateStatus, "Prepare a context packet first.", "is-error");
      return;
    }
    if (runtime.generating) {
      return;
    }
    if (config.agentEnabled && !disclosureAccepted) {
      disclosureAccepted = runtime.confirmer(DISCLOSURE);
      if (!disclosureAccepted) {
        setStatusText(view.generateStatus, "Generation canceled.", "");
        return;
      }
    }
    runtime.generating = true;
    view.generateButton.disabled = true;
    view.candidatePanel.setAttribute("aria-busy", "true");
    setStatusText(view.generateStatus, "Generating a candidate draft…", "is-loading");
    try {
      const response = await requestSynthesisGenerate(runtime.fetcher, config.generateEndpoint, {
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
      runtime.render();
      runtime.persist();
      setStatusText(
        view.generateStatus,
        "Candidate ready. Your draft is unchanged until you replace or append it.",
        "is-success"
      );
      view.candidatePanel.focus({ preventScroll: true });
    } catch (error) {
      if (errorCode(error) === "not_found") {
        runtime.forgetContext(view.generateStatus);
        return;
      }
      setStatusText(
        view.generateStatus,
        errorMessage(error, "The generation command failed."),
        "is-error"
      );
    } finally {
      runtime.generating = false;
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
      mode === "replace" || existing === ""
        ? candidate.markdown
        : `${existing}\n\n${candidate.markdown}`;
    view.draftInput.value = model.draft;
    invalidateSynthesisPreview(model);
    runtime.render();
    runtime.persist();
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
    if (!hasWritableClipboard(runtime.clipboard)) {
      setStatusText(
        view.generateStatus,
        "Clipboard unavailable. Select the candidate Markdown above and copy it manually.",
        "is-error"
      );
      return;
    }
    view.candidateCopyButton.disabled = true;
    try {
      await runtime.clipboard.writeText(candidate.markdown);
      setStatusText(view.generateStatus, "Candidate copied.", "is-success");
    } catch {
      setStatusText(
        view.generateStatus,
        "Copy failed. Select the candidate Markdown above and copy it manually.",
        "is-error"
      );
    } finally {
      view.candidateCopyButton.disabled = !hasWritableClipboard(runtime.clipboard);
    }
  };

  const dismissCandidate = (): void => {
    if (model.candidate === null) {
      return;
    }
    model.candidate = null;
    runtime.render();
    runtime.persist();
    setStatusText(view.generateStatus, "Candidate dismissed. Your draft is unchanged.", "");
    view.generateButton.focus({ preventScroll: true });
  };

  return {
    prepareContext,
    copyContext,
    generate,
    useCandidate,
    copyCandidate,
    dismissCandidate,
  };
};
