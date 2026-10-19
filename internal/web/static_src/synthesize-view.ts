import { synthesisDisclosure, synthesisNavigation } from "./synthesize-layout";
import { element } from "./synthesize-view-elements";
import {
  buildDraftSection,
  buildSearchSection,
  buildSourcesSection,
  buildSuggestionsSection,
} from "./synthesize-view-sections";
import {
  buildContextSection,
  buildCreateSection,
  buildGenerateSection,
} from "./synthesize-view-panels";
import type { SynthesisConfig } from "./synthesize-model";
import type { RelatedMode } from "./related-types";

export type SynthesisView = {
  root: HTMLElement;
  warning: HTMLElement;
  nextStep: HTMLElement;
  nextStepText: HTMLElement;
  nextStepAction: HTMLButtonElement;
  sourceList: HTMLOListElement;
  selectionStatus: HTMLElement;
  suggestionTabs: Record<RelatedMode, HTMLButtonElement>;
  suggestionPanels: Record<RelatedMode, HTMLElement>;
  suggestionResults: Record<RelatedMode, HTMLElement>;
  suggestionStatus: HTMLElement;
  searchForm: HTMLFormElement;
  searchInput: HTMLInputElement;
  searchResults: HTMLElement;
  searchStatus: HTMLElement;
  titleInput: HTMLInputElement;
  titleError: HTMLElement;
  folderInput: HTMLInputElement;
  folderError: HTMLElement;
  purposeSelect: HTMLSelectElement;
  promptInput: HTMLTextAreaElement;
  draftInput: HTMLTextAreaElement;
  draftError: HTMLElement;
  prepareButton: HTMLButtonElement;
  copyButton: HTMLButtonElement;
  discardButton: HTMLButtonElement;
  contextStatus: HTMLElement;
  contextSummary: HTMLElement;
  manifest: HTMLElement;
  packet: HTMLElement;
  generateButton: HTMLButtonElement;
  generateStatus: HTMLElement;
  candidatePanel: HTMLElement;
  candidateRendered: HTMLElement;
  candidateMarkdown: HTMLElement;
  candidateReplaceButton: HTMLButtonElement;
  candidateAppendButton: HTMLButtonElement;
  candidateCopyButton: HTMLButtonElement;
  candidateDismissButton: HTMLButtonElement;
  destination: HTMLElement;
  previewButton: HTMLButtonElement;
  applyButton: HTMLButtonElement;
  createHint: HTMLElement;
  previewStatus: HTMLElement;
  previewPanel: HTMLElement;
  previewDestination: HTMLElement;
  previewExpiry: HTMLElement;
  previewRendered: HTMLElement;
  previewMarkdown: HTMLElement;
  previewSources: HTMLOListElement;
};

const bindContextShortcut = (button: HTMLButtonElement, context: HTMLDetailsElement): void => {
  button.addEventListener("click", () => {
    context.open = true;
    context.querySelector("summary")?.focus();
  });
};

export const createSynthesisView = (
  mount: HTMLElement,
  config: SynthesisConfig
): SynthesisView => {
  const doc = mount.ownerDocument;
  const warning = element(doc, "p", "synthesize-warning", {
    "data-synthesize-warning": "",
    role: "status",
    "aria-live": "polite",
  });
  warning.hidden = true;
  const nextStep = element(doc, "p", "synthesize-next-step", {
    "data-synthesize-next-step": "",
  });
  const nextStepText = element(doc, "span", "synthesize-next-step-text");
  const nextStepAction = element(doc, "button", "synthesize-next-step-action", { type: "button" });
  nextStepAction.textContent = "Open context";
  nextStep.append(nextStepText, nextStepAction);
  const sources = buildSourcesSection(doc);
  const suggestions = buildSuggestionsSection(doc);
  const search = buildSearchSection(doc);
  const draft = buildDraftSection(doc, config);
  const context = buildContextSection(doc);
  const generate = buildGenerateSection(doc, config);
  const create = buildCreateSection(doc);

  const columns = element(doc, "div", "synthesize-columns");
  const left = element(doc, "div", "synthesize-column");
  const discovery = synthesisDisclosure(doc, "Find supporting notes", suggestions.section, search.section);
  const sourceDetails = synthesisDisclosure(doc, "1. Choose sources", sources.section, discovery);
  sourceDetails.open = doc.defaultView?.matchMedia?.("(min-width: 800px)").matches ?? false;
  const contextDetails = synthesisDisclosure(doc, "2. Prepare context", context.section);
  // A narrow phone still opens on the draft. The prompt above it opens this
  // required step directly when the person is ready to prepare the packet.
  contextDetails.open = doc.defaultView?.matchMedia?.("(min-width: 961px)").matches ?? true;
  bindContextShortcut(nextStepAction, contextDetails);
  left.append(sourceDetails, contextDetails);
  const right = element(doc, "div", "synthesize-column");
  const generation = synthesisDisclosure(doc, "Ask AI · optional", generate.section);
  const agentStatus = mount.closest("[data-synthesize-root]")?.querySelector<HTMLElement>("[data-synthesize-agent-status]");
  if (agentStatus) { agentStatus.hidden = false; generate.section.prepend(agentStatus); }
  right.append(draft.section, generation, create.section);
  const navigation = synthesisNavigation(doc, [["Sources", sourceDetails], ["Context", contextDetails], ["Draft", draft.section], ["Review", create.section]]);
  columns.append(left, right);

  mount.replaceChildren(warning, nextStep, navigation, columns);
  return {
    root: mount,
    warning,
    nextStep,
    nextStepText,
    nextStepAction,
    sourceList: sources.list,
    selectionStatus: sources.status,
    suggestionTabs: suggestions.tabs,
    suggestionPanels: suggestions.panels,
    suggestionResults: suggestions.results,
    suggestionStatus: suggestions.status,
    searchForm: search.form,
    searchInput: search.input,
    searchResults: search.results,
    searchStatus: search.status,
    titleInput: draft.title,
    titleError: draft.titleError,
    folderInput: draft.folder,
    folderError: draft.folderError,
    purposeSelect: draft.purpose,
    promptInput: draft.prompt,
    draftInput: draft.draft,
    draftError: draft.draftError,
    prepareButton: context.prepare,
    copyButton: context.copy,
    discardButton: context.discard,
    contextStatus: context.status,
    contextSummary: context.summary,
    manifest: context.manifest,
    packet: context.packet,
    generateButton: generate.generate,
    generateStatus: generate.status,
    candidatePanel: generate.panel,
    candidateRendered: generate.rendered,
    candidateMarkdown: generate.markdown,
    candidateReplaceButton: generate.replace,
    candidateAppendButton: generate.append,
    candidateCopyButton: generate.copy,
    candidateDismissButton: generate.dismiss,
    destination: create.destination,
    previewButton: create.preview,
    applyButton: create.apply,
    createHint: create.hint,
    previewStatus: create.status,
    previewPanel: create.panel,
    previewDestination: create.previewDestination,
    previewExpiry: create.expiry,
    previewRendered: create.rendered,
    previewMarkdown: create.markdown,
    previewSources: create.sources,
  };
};
