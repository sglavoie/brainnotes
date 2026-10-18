import { relatedModeDefinitions } from "./related-model";
import { invalidateSynthesisContext, invalidateSynthesisPreview } from "./synthesize-model";
import type { SynthesisRuntime } from "./synthesize-runtime";
import type { createSynthesisSelectionActions } from "./synthesize-selection";
import type { createSynthesisDiscoveryActions } from "./synthesize-discovery";
import type { createSynthesisContextActions } from "./synthesize-context-actions";
import type { createSynthesisCreateActions } from "./synthesize-create-actions";

export const bindSynthesisSelection = (runtime: SynthesisRuntime, mount: HTMLElement, selection: ReturnType<typeof createSynthesisSelectionActions>): void => {
  const { view } = runtime;
  view.sourceList.addEventListener("click", (event) => {
    const target = event.target instanceof Element ? event.target : null;
    const remove = target?.closest<HTMLButtonElement>("[data-synthesize-remove]");
    if (remove) {
      selection.removeSource(remove.getAttribute("data-synthesize-remove") || "");
    }
  });

  view.sourceList.addEventListener("keydown", (event) => {
    const target = event.target instanceof Element ? event.target : null;
    const handle = target?.closest<HTMLButtonElement>("[data-synthesize-handle]");
    if (!handle || (event.key !== "ArrowUp" && event.key !== "ArrowDown")) {
      return;
    }
    event.preventDefault();
    selection.moveSource(
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
    selection.startDrag(row, event);
  });

  mount.addEventListener("click", (event) => {
    const target = event.target instanceof Element ? event.target : null;
    const add = target?.closest<HTMLButtonElement>("[data-synthesize-add]");
    if (!add || add.disabled) {
      return;
    }
    selection.addSource(add.getAttribute("data-synthesize-add") || "");
  });

};

export const bindSynthesisDiscovery = (runtime: SynthesisRuntime, discovery: ReturnType<typeof createSynthesisDiscoveryActions>): void => {
  const { view } = runtime;
  for (const definition of relatedModeDefinitions) {
    const tab = view.suggestionTabs[definition.mode];
    tab.addEventListener("click", () => {
      void discovery.selectMode(definition.mode);
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
      void discovery.selectMode(next);
    });
  }

  view.searchForm.addEventListener("submit", (event) => {
    event.preventDefault();
    void discovery.runSearch(view.searchInput.value);
  });

};

export const bindSynthesisFields = (runtime: SynthesisRuntime): void => {
  const { view, model } = runtime;
  // Title, folder, and draft never reach the context packet, so they invalidate
  // the receipt that names the exact bytes and leave the packet alone.
  view.titleInput.addEventListener("input", () => {
    model.title = view.titleInput.value;
    invalidateSynthesisPreview(model);
    runtime.render();
    runtime.persist();
  });
  view.folderInput.addEventListener("input", () => {
    model.folder = view.folderInput.value;
    invalidateSynthesisPreview(model);
    runtime.render();
    runtime.persist();
  });
  view.draftInput.addEventListener("input", () => {
    model.draft = view.draftInput.value;
    invalidateSynthesisPreview(model);
    runtime.render();
    runtime.persist();
  });
  view.purposeSelect.addEventListener("change", () => {
    model.purpose = view.purposeSelect.value;
    invalidateSynthesisContext(model);
    runtime.render();
    runtime.persist();
  });
  view.promptInput.addEventListener("input", () => {
    model.prompt = view.promptInput.value;
    invalidateSynthesisContext(model);
    runtime.render();
    runtime.persist();
  });

};

export const bindSynthesisActions = (runtime: SynthesisRuntime, context: ReturnType<typeof createSynthesisContextActions>, create: ReturnType<typeof createSynthesisCreateActions>): void => {
  const { view } = runtime;
  view.prepareButton.addEventListener("click", () => {
    void context.prepareContext();
  });
  view.copyButton.addEventListener("click", () => {
    void context.copyContext();
  });
  view.discardButton.addEventListener("click", () => {
    create.discard();
  });
  view.generateButton.addEventListener("click", () => {
    void context.generate();
  });
  view.candidateReplaceButton.addEventListener("click", () => {
    context.useCandidate("replace");
  });
  view.candidateAppendButton.addEventListener("click", () => {
    context.useCandidate("append");
  });
  view.candidateCopyButton.addEventListener("click", () => {
    void context.copyCandidate();
  });
  view.candidateDismissButton.addEventListener("click", () => {
    context.dismissCandidate();
  });
  view.previewButton.addEventListener("click", () => {
    void create.preview();
  });
  view.applyButton.addEventListener("click", () => {
    void create.apply();
  });

};

