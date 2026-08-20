import { relatedModeDefinitions } from "./related-model";
import { setStatusText, type StatusKind } from "./status";
import {
  formatSynthesisBytes,
  formatSynthesisExpiry,
  synthesisDestinationPath,
  synthesisPurposeLabel,
  type SynthesisConfig,
  type SynthesisModel,
} from "./synthesize-model";
import type { RelatedMode } from "./related-types";
import type { SynthesisCandidate } from "./synthesize-sources";

export type SynthesisView = {
  root: HTMLElement;
  warning: HTMLElement;
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
  previewStatus: HTMLElement;
  previewPanel: HTMLElement;
  previewDestination: HTMLElement;
  previewExpiry: HTMLElement;
  previewRendered: HTMLElement;
  previewMarkdown: HTMLElement;
  previewSources: HTMLOListElement;
};

const suggestionID = (kind: "tab" | "panel", mode: RelatedMode): string =>
  `synthesize-${kind}-${mode}`;

const element = <K extends keyof HTMLElementTagNameMap>(
  doc: Document,
  tag: K,
  className: string,
  attributes: Record<string, string> = {}
): HTMLElementTagNameMap[K] => {
  const node = doc.createElement(tag);
  if (className !== "") {
    node.className = className;
  }
  for (const [name, value] of Object.entries(attributes)) {
    node.setAttribute(name, value);
  }
  return node;
};

const labelled = (
  doc: Document,
  id: string,
  text: string,
  control: HTMLElement
): HTMLElement => {
  const wrapper = element(doc, "div", "synthesize-field");
  const label = element(doc, "label", "synthesize-field-label");
  label.setAttribute("for", id);
  label.textContent = text;
  control.id = id;
  wrapper.append(label, control);
  return wrapper;
};

// A field error is announced by the control that owns it rather than by a live
// region, so a validation message reaches a screen reader when focus lands on
// the offending field instead of interrupting whatever it was reading.
const labelledWithError = (
  doc: Document,
  id: string,
  text: string,
  control: HTMLElement,
  errorAttribute: string
): { wrapper: HTMLElement; error: HTMLElement } => {
  const wrapper = labelled(doc, id, text, control);
  const error = element(doc, "p", "synthesize-field-error", { [errorAttribute]: "" });
  error.id = `${id}-error`;
  error.hidden = true;
  control.setAttribute("aria-describedby", error.id);
  wrapper.appendChild(error);
  return { wrapper, error };
};

const buildSourcesSection = (
  doc: Document
): { section: HTMLElement; list: HTMLOListElement; status: HTMLElement } => {
  const section = element(doc, "section", "synthesize-section synthesize-sources", {
    "aria-labelledby": "synthesize-sources-heading",
  });
  const heading = element(doc, "h3", "synthesize-section-heading");
  heading.id = "synthesize-sources-heading";
  heading.textContent = "Sources";
  const intro = element(doc, "p", "synthesize-section-intro");
  intro.textContent =
    "The anchor is always first. Drag a handle or press Up and Down on it to reorder supporting notes.";
  const list = element(doc, "ol", "synthesize-source-list", {
    "data-synthesize-source-list": "",
  });
  const status = element(doc, "p", "synthesize-selection-status", {
    "data-synthesize-selection-status": "",
  });
  status.tabIndex = -1;
  section.append(heading, intro, list, status);
  return { section, list, status };
};

const buildSuggestionsSection = (
  doc: Document
): {
  section: HTMLElement;
  tabs: Record<RelatedMode, HTMLButtonElement>;
  panels: Record<RelatedMode, HTMLElement>;
  results: Record<RelatedMode, HTMLElement>;
  status: HTMLElement;
} => {
  const section = element(doc, "section", "synthesize-section synthesize-suggestions", {
    "aria-labelledby": "synthesize-suggestions-heading",
  });
  const heading = element(doc, "h3", "synthesize-section-heading");
  heading.id = "synthesize-suggestions-heading";
  heading.textContent = "Suggested notes";
  const tablist = element(doc, "div", "synthesize-tab-list", {
    role: "tablist",
    "aria-label": "Suggestion modes",
  });
  const status = element(doc, "p", "synthesize-suggestions-status", {
    "data-synthesize-suggestions-status": "",
    role: "status",
    "aria-live": "polite",
  });
  const panelsContainer = element(doc, "div", "synthesize-suggestion-panels");
  const tabs = {} as Record<RelatedMode, HTMLButtonElement>;
  const panels = {} as Record<RelatedMode, HTMLElement>;
  const results = {} as Record<RelatedMode, HTMLElement>;

  for (const definition of relatedModeDefinitions) {
    const active = definition.mode === "balanced";
    const tab = element(doc, "button", "synthesize-tab", {
      role: "tab",
      "aria-controls": suggestionID("panel", definition.mode),
      "aria-selected": active ? "true" : "false",
      "data-synthesize-tab": definition.mode,
    });
    tab.type = "button";
    tab.id = suggestionID("tab", definition.mode);
    tab.tabIndex = active ? 0 : -1;
    tab.textContent = definition.label;
    tablist.appendChild(tab);

    const panel = element(doc, "div", "synthesize-suggestion-panel", {
      role: "tabpanel",
      "aria-labelledby": suggestionID("tab", definition.mode),
      "data-synthesize-panel": definition.mode,
    });
    panel.id = suggestionID("panel", definition.mode);
    panel.hidden = !active;
    const description = element(doc, "p", "synthesize-tab-description");
    description.textContent = definition.description;
    const list = element(doc, "div", "synthesize-candidates", {
      "data-synthesize-suggestions": definition.mode,
    });
    panel.append(description, list);
    panelsContainer.appendChild(panel);

    tabs[definition.mode] = tab;
    panels[definition.mode] = panel;
    results[definition.mode] = list;
  }

  section.append(heading, tablist, status, panelsContainer);
  return { section, tabs, panels, results, status };
};

const buildSearchSection = (
  doc: Document
): {
  section: HTMLElement;
  form: HTMLFormElement;
  input: HTMLInputElement;
  results: HTMLElement;
  status: HTMLElement;
} => {
  const section = element(doc, "section", "synthesize-section synthesize-search", {
    "aria-labelledby": "synthesize-search-heading",
  });
  const heading = element(doc, "h3", "synthesize-section-heading");
  heading.id = "synthesize-search-heading";
  heading.textContent = "Search the vault";
  const form = element(doc, "form", "synthesize-search-form", {
    "data-synthesize-search-form": "",
    role: "search",
  });
  const input = element(doc, "input", "synthesize-search-input", {
    "data-synthesize-search-input": "",
    type: "search",
    placeholder: "Find a note to add",
  });
  input.name = "q";
  const submit = element(doc, "button", "synthesize-search-submit");
  submit.type = "submit";
  submit.textContent = "Search";
  form.append(labelled(doc, "synthesize-search-query", "Search notes", input), submit);
  const status = element(doc, "p", "synthesize-search-status", {
    "data-synthesize-search-status": "",
    role: "status",
    "aria-live": "polite",
  });
  const results = element(doc, "div", "synthesize-candidates", {
    "data-synthesize-search-results": "",
  });
  section.append(heading, form, status, results);
  return { section, form, input, results, status };
};

const buildDraftSection = (
  doc: Document,
  config: SynthesisConfig
): {
  section: HTMLElement;
  title: HTMLInputElement;
  titleError: HTMLElement;
  folder: HTMLInputElement;
  folderError: HTMLElement;
  purpose: HTMLSelectElement;
  prompt: HTMLTextAreaElement;
  draft: HTMLTextAreaElement;
  draftError: HTMLElement;
} => {
  const section = element(doc, "section", "synthesize-section synthesize-draft", {
    "aria-labelledby": "synthesize-draft-heading",
  });
  const heading = element(doc, "h3", "synthesize-section-heading");
  heading.id = "synthesize-draft-heading";
  heading.textContent = "New note";

  const title = element(doc, "input", "synthesize-input", {
    "data-synthesize-title": "",
    type: "text",
  });
  title.name = "title";
  const folder = element(doc, "input", "synthesize-input", {
    "data-synthesize-folder": "",
    type: "text",
    placeholder: "Vault root",
  });
  folder.name = "folder";
  const purpose = element(doc, "select", "synthesize-input", {
    "data-synthesize-purpose": "",
  });
  purpose.name = "purpose";
  for (const value of config.purposes) {
    const option = doc.createElement("option");
    option.value = value;
    option.textContent = synthesisPurposeLabel(value);
    purpose.appendChild(option);
  }
  const prompt = element(doc, "textarea", "synthesize-textarea", {
    "data-synthesize-prompt": "",
    rows: "3",
    placeholder: "Optional question or angle for this research packet",
  });
  prompt.name = "prompt";
  const draft = element(doc, "textarea", "synthesize-textarea synthesize-draft-body", {
    "data-synthesize-draft": "",
    rows: "12",
    placeholder: "Write the note that says what these sources add up to",
  });
  draft.name = "draft";

  const titleField = labelledWithError(
    doc,
    "synthesize-title",
    "Title",
    title,
    "data-synthesize-title-error"
  );
  const folderField = labelledWithError(
    doc,
    "synthesize-folder",
    "Folder",
    folder,
    "data-synthesize-folder-error"
  );
  const draftField = labelledWithError(
    doc,
    "synthesize-draft",
    "Draft",
    draft,
    "data-synthesize-draft-error"
  );
  const grid = element(doc, "div", "synthesize-field-grid");
  grid.append(titleField.wrapper, folderField.wrapper, labelled(doc, "synthesize-purpose", "Purpose", purpose));
  section.append(
    heading,
    grid,
    labelled(doc, "synthesize-prompt", "Prompt", prompt),
    draftField.wrapper
  );
  return {
    section,
    title,
    titleError: titleField.error,
    folder,
    folderError: folderField.error,
    purpose,
    prompt,
    draft,
    draftError: draftField.error,
  };
};

const buildGenerateSection = (
  doc: Document,
  config: SynthesisConfig
): {
  section: HTMLElement;
  generate: HTMLButtonElement;
  status: HTMLElement;
  panel: HTMLElement;
  rendered: HTMLElement;
  markdown: HTMLElement;
  replace: HTMLButtonElement;
  append: HTMLButtonElement;
  copy: HTMLButtonElement;
  dismiss: HTMLButtonElement;
} => {
  const section = element(doc, "section", "synthesize-section synthesize-generate", {
    "aria-labelledby": "synthesize-generate-heading",
  });
  const heading = element(doc, "h3", "synthesize-section-heading");
  heading.id = "synthesize-generate-heading";
  heading.textContent = "Generated candidate";
  const intro = element(doc, "p", "synthesize-section-intro");
  intro.textContent = config.agentEnabled
    ? "Generation sends the prepared context packet to your configured [agent].command. The candidate is shown here and never replaces your draft on its own."
    : "Generation is unavailable until [agent].command is configured. Everything else on this page works without an agent.";

  const actions = element(doc, "div", "synthesize-generate-actions");
  const generate = element(doc, "button", "synthesize-action", {
    "data-synthesize-generate": "",
  });
  generate.type = "button";
  generate.textContent = "Generate draft";
  generate.disabled = true;
  actions.appendChild(generate);

  const status = element(doc, "p", "synthesize-generate-status", {
    "data-synthesize-generate-status": "",
    role: "status",
    "aria-live": "polite",
  });

  const panel = element(doc, "article", "synthesize-candidate-panel", {
    "data-synthesize-candidate-panel": "",
    "aria-labelledby": "synthesize-generate-heading",
  });
  panel.hidden = true;
  panel.tabIndex = -1;
  const rendered = element(doc, "div", "synthesize-candidate-rendered", {
    "data-synthesize-candidate-rendered": "",
  });
  const markdownWrapper = element(doc, "details", "synthesize-candidate-markdown");
  const markdownSummary = doc.createElement("summary");
  markdownSummary.textContent = "Candidate Markdown";
  const markdown = element(doc, "pre", "synthesize-packet-body", {
    "data-synthesize-candidate-markdown": "",
    tabindex: "0",
  });
  markdownWrapper.append(markdownSummary, markdown);

  const candidateActions = element(doc, "div", "synthesize-candidate-actions");
  const replace = element(doc, "button", "synthesize-action", {
    "data-synthesize-candidate-replace": "",
  });
  replace.type = "button";
  replace.textContent = "Replace draft";
  const append = element(doc, "button", "synthesize-action", {
    "data-synthesize-candidate-append": "",
  });
  append.type = "button";
  append.textContent = "Append to draft";
  const copy = element(doc, "button", "synthesize-action", {
    "data-synthesize-candidate-copy": "",
  });
  copy.type = "button";
  copy.textContent = "Copy candidate";
  const dismiss = element(doc, "button", "synthesize-action is-quiet", {
    "data-synthesize-candidate-dismiss": "",
  });
  dismiss.type = "button";
  dismiss.textContent = "Dismiss candidate";
  candidateActions.append(replace, append, copy, dismiss);
  panel.append(rendered, markdownWrapper, candidateActions);

  section.append(heading, intro, actions, status, panel);
  return { section, generate, status, panel, rendered, markdown, replace, append, copy, dismiss };
};

const buildCreateSection = (
  doc: Document
): {
  section: HTMLElement;
  destination: HTMLElement;
  preview: HTMLButtonElement;
  apply: HTMLButtonElement;
  status: HTMLElement;
  panel: HTMLElement;
  previewDestination: HTMLElement;
  expiry: HTMLElement;
  rendered: HTMLElement;
  markdown: HTMLElement;
  sources: HTMLOListElement;
} => {
  const section = element(doc, "section", "synthesize-section synthesize-create", {
    "aria-labelledby": "synthesize-create-heading",
  });
  const heading = element(doc, "h3", "synthesize-section-heading");
  heading.id = "synthesize-create-heading";
  heading.textContent = "Create note";
  const destination = element(doc, "p", "synthesize-destination", {
    "data-synthesize-destination": "",
  });

  const actions = element(doc, "div", "synthesize-create-actions");
  const preview = element(doc, "button", "synthesize-action is-primary", {
    "data-synthesize-preview": "",
  });
  preview.type = "button";
  preview.textContent = "Preview note";
  const apply = element(doc, "button", "synthesize-action", { "data-synthesize-apply": "" });
  apply.type = "button";
  apply.textContent = "Create note";
  apply.disabled = true;
  actions.append(preview, apply);

  const status = element(doc, "p", "synthesize-preview-status", {
    "data-synthesize-preview-status": "",
    role: "status",
    "aria-live": "polite",
  });

  const panel = element(doc, "div", "synthesize-preview-panel", {
    "data-synthesize-preview-panel": "",
  });
  panel.hidden = true;
  panel.tabIndex = -1;
  const previewDestination = element(doc, "p", "synthesize-preview-destination", {
    "data-synthesize-preview-destination": "",
  });
  const expiry = element(doc, "p", "synthesize-preview-expiry", {
    "data-synthesize-preview-expiry": "",
  });
  const rendered = element(doc, "div", "synthesize-preview-rendered", {
    "data-synthesize-preview-rendered": "",
  });
  const markdownWrapper = element(doc, "details", "synthesize-preview-markdown");
  const markdownSummary = doc.createElement("summary");
  markdownSummary.textContent = "Note Markdown";
  const markdown = element(doc, "pre", "synthesize-packet-body", {
    "data-synthesize-preview-markdown": "",
    tabindex: "0",
  });
  markdownWrapper.append(markdownSummary, markdown);
  const sourcesHeading = element(doc, "h4", "synthesize-manifest-heading");
  sourcesHeading.textContent = "Sources";
  const sources = element(doc, "ol", "synthesize-preview-sources", {
    "data-synthesize-preview-sources": "",
  });
  panel.append(previewDestination, expiry, rendered, markdownWrapper, sourcesHeading, sources);

  section.append(heading, destination, actions, status, panel);
  return {
    section,
    destination,
    preview,
    apply,
    status,
    panel,
    previewDestination,
    expiry,
    rendered,
    markdown,
    sources,
  };
};

const buildContextSection = (
  doc: Document
): {
  section: HTMLElement;
  prepare: HTMLButtonElement;
  copy: HTMLButtonElement;
  discard: HTMLButtonElement;
  status: HTMLElement;
  summary: HTMLElement;
  manifest: HTMLElement;
  packet: HTMLElement;
} => {
  const section = element(doc, "section", "synthesize-section synthesize-context", {
    "aria-labelledby": "synthesize-context-heading",
  });
  const heading = element(doc, "h3", "synthesize-section-heading");
  heading.id = "synthesize-context-heading";
  heading.textContent = "Context packet";
  const actions = element(doc, "div", "synthesize-context-actions");
  const prepare = element(doc, "button", "synthesize-action is-primary", {
    "data-synthesize-prepare": "",
  });
  prepare.type = "button";
  prepare.textContent = "Prepare context";
  const copy = element(doc, "button", "synthesize-action", { "data-synthesize-copy": "" });
  copy.type = "button";
  copy.textContent = "Copy context";
  copy.disabled = true;
  const discard = element(doc, "button", "synthesize-action is-quiet", {
    "data-synthesize-discard": "",
  });
  discard.type = "button";
  discard.textContent = "Discard saved work";
  actions.append(prepare, copy, discard);
  const status = element(doc, "p", "synthesize-context-status", {
    "data-synthesize-context-status": "",
    role: "status",
    "aria-live": "polite",
  });
  const summary = element(doc, "p", "synthesize-context-summary", {
    "data-synthesize-context-summary": "",
  });
  const manifest = element(doc, "div", "synthesize-manifest", {
    "data-synthesize-manifest": "",
  });
  const packetWrapper = element(doc, "details", "synthesize-packet");
  const packetSummary = doc.createElement("summary");
  packetSummary.textContent = "Packet Markdown";
  const packet = element(doc, "pre", "synthesize-packet-body", {
    "data-synthesize-packet": "",
    tabindex: "0",
  });
  packetWrapper.append(packetSummary, packet);
  section.append(heading, actions, status, summary, manifest, packetWrapper);
  return { section, prepare, copy, discard, status, summary, manifest, packet };
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
  const sources = buildSourcesSection(doc);
  const suggestions = buildSuggestionsSection(doc);
  const search = buildSearchSection(doc);
  const draft = buildDraftSection(doc, config);
  const context = buildContextSection(doc);
  const generate = buildGenerateSection(doc, config);
  const create = buildCreateSection(doc);

  const columns = element(doc, "div", "synthesize-columns");
  const left = element(doc, "div", "synthesize-column");
  left.append(sources.section, suggestions.section, search.section);
  const right = element(doc, "div", "synthesize-column");
  right.append(draft.section, context.section, generate.section, create.section);
  columns.append(left, right);

  mount.replaceChildren(warning, columns);
  return {
    root: mount,
    warning,
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
    previewStatus: create.status,
    previewPanel: create.panel,
    previewDestination: create.previewDestination,
    previewExpiry: create.expiry,
    previewRendered: create.rendered,
    previewMarkdown: create.markdown,
    previewSources: create.sources,
  };
};

export const selectSynthesisSuggestionMode = (
  view: SynthesisView,
  activeMode: RelatedMode
): void => {
  for (const definition of relatedModeDefinitions) {
    const selected = definition.mode === activeMode;
    view.suggestionTabs[definition.mode].setAttribute(
      "aria-selected",
      selected ? "true" : "false"
    );
    view.suggestionTabs[definition.mode].tabIndex = selected ? 0 : -1;
    view.suggestionPanels[definition.mode].hidden = !selected;
  }
};

const sourceRow = (
  doc: Document,
  position: number,
  total: number,
  source: { key: string; title: string; path: string; route: string },
  anchor: boolean
): HTMLLIElement => {
  const row = element(doc, "li", "synthesize-source", {
    "data-synthesize-source": source.key,
    "data-synthesize-position": String(position),
  });
  if (anchor) {
    row.classList.add("is-anchor");
    row.setAttribute("data-synthesize-anchor", "");
  }
  const badge = element(doc, "span", "synthesize-source-position");
  badge.textContent = String(position);
  const body = element(doc, "div", "synthesize-source-body");
  const link = element(doc, "a", "synthesize-source-title");
  link.href = source.route;
  link.textContent = source.title;
  const path = element(doc, "code", "synthesize-source-path");
  path.textContent = source.path;
  body.append(link, path);
  row.append(badge, body);

  if (anchor) {
    const note = element(doc, "span", "synthesize-source-role");
    note.textContent = "Anchor";
    row.appendChild(note);
    return row;
  }

  const controls = element(doc, "div", "synthesize-source-controls");
  const handle = element(doc, "button", "synthesize-source-handle", {
    "data-synthesize-handle": source.key,
    "aria-label": `Reorder ${source.title}, position ${position} of ${total}. Press Up or Down to move it.`,
  });
  handle.type = "button";
  handle.textContent = "Drag";
  const remove = element(doc, "button", "synthesize-source-remove", {
    "data-synthesize-remove": source.key,
    "aria-label": `Remove ${source.title}`,
  });
  remove.type = "button";
  remove.textContent = "Remove";
  controls.append(handle, remove);
  row.appendChild(controls);
  return row;
};

export const renderSynthesisSources = (
  view: SynthesisView,
  model: SynthesisModel,
  config: SynthesisConfig
): void => {
  const doc = view.sourceList.ownerDocument;
  const total = model.sources.length + 1;
  const rows: HTMLLIElement[] = [sourceRow(doc, 1, total, config.anchor, true)];
  model.sources.forEach((source, index) => {
    rows.push(sourceRow(doc, index + 2, total, source, false));
  });
  view.sourceList.replaceChildren(...rows);
  const remaining = config.maxSupporters - model.sources.length;
  view.selectionStatus.textContent =
    remaining > 0
      ? `${total} of ${config.maxSupporters + 1} notes selected. ${remaining} more can be added.`
      : `${total} of ${config.maxSupporters + 1} notes selected. Remove one to add another.`;
};

export const renderSynthesisCandidates = (
  container: HTMLElement,
  candidates: SynthesisCandidate[],
  selectedKeys: string[],
  anchorKey: string,
  detailIsHTML: boolean,
  emptyMessage: string
): void => {
  const doc = container.ownerDocument;
  const selected = new Set(selectedKeys);
  const rows = candidates
    .filter((candidate) => candidate.key !== anchorKey && candidate.key !== "")
    .map((candidate) => {
      const card = element(doc, "article", "synthesize-candidate", {
        "data-synthesize-candidate": candidate.key,
      });
      const heading = element(doc, "h4", "synthesize-candidate-title");
      const link = element(doc, "a", "");
      link.href = candidate.route;
      link.textContent = candidate.title;
      heading.appendChild(link);
      const path = element(doc, "p", "synthesize-candidate-path");
      const pathCode = doc.createElement("code");
      pathCode.textContent = candidate.path;
      path.appendChild(pathCode);
      card.append(heading, path);
      if (candidate.detail !== "") {
        const detail = element(doc, "p", "synthesize-candidate-detail");
        if (detailIsHTML) {
          detail.innerHTML = candidate.detail;
        } else {
          detail.textContent = candidate.detail;
        }
        card.appendChild(detail);
      }
      const add = element(doc, "button", "synthesize-candidate-add", {
        "data-synthesize-add": candidate.key,
      });
      add.type = "button";
      if (selected.has(candidate.key)) {
        add.disabled = true;
        add.textContent = "Selected";
      } else {
        add.textContent = "Add source";
        add.setAttribute("aria-label", `Add ${candidate.title} as a source`);
      }
      card.appendChild(add);
      return card;
    });
  if (rows.length === 0) {
    const empty = element(doc, "p", "synthesize-candidates-empty");
    empty.textContent = emptyMessage;
    container.replaceChildren(empty);
    return;
  }
  container.replaceChildren(...rows);
};

export type SynthesisWarningLink = { href: string; label: string };

export const setSynthesisWarning = (
  view: SynthesisView,
  message: string,
  link?: SynthesisWarningLink
): void => {
  view.warning.textContent = message;
  if (message !== "" && link) {
    const anchor = element(view.warning.ownerDocument, "a", "synthesize-warning-link", {
      "data-synthesize-warning-link": "",
      href: link.href,
    });
    anchor.textContent = link.label;
    view.warning.append(" ", anchor);
  }
  view.warning.hidden = message === "";
};

export const setSynthesisStatus = (
  target: HTMLElement,
  message: string,
  kind: StatusKind = ""
): void => {
  setStatusText(target, message, kind);
};

const excerptLabel = (path: string, start: number, end: number): string =>
  start === end ? `${path}:${start}` : `${path}:${start}-${end}`;

export const renderSynthesisContext = (view: SynthesisView, model: SynthesisModel): void => {
  const doc = view.manifest.ownerDocument;
  view.prepareButton.textContent =
    model.context === null && !model.contextStale ? "Prepare context" : "Rebuild context";
  if (model.context === null) {
    view.copyButton.disabled = true;
    view.contextSummary.textContent = "";
    view.manifest.replaceChildren();
    view.packet.textContent = "";
    return;
  }
  const context = model.context;
  view.copyButton.disabled = false;
  view.contextSummary.textContent = `${context.manifest.notes.length} notes, ${formatSynthesisBytes(context.manifest.byte_count)} of a ${formatSynthesisBytes(context.manifest.max_bytes)} budget, purpose ${synthesisPurposeLabel(context.purpose)}.`;

  const notesHeading = element(doc, "h4", "synthesize-manifest-heading");
  notesHeading.textContent = "Selected notes";
  const notes = element(doc, "ol", "synthesize-manifest-notes");
  for (const note of context.manifest.notes) {
    const item = doc.createElement("li");
    const link = doc.createElement("a");
    link.href = note.route;
    link.textContent = note.title || note.path;
    const path = doc.createElement("code");
    path.textContent = note.path;
    item.append(link, " ", path);
    notes.appendChild(item);
  }
  const excerptsHeading = element(doc, "h4", "synthesize-manifest-heading");
  excerptsHeading.textContent = "Excerpt locations";
  const excerpts = element(doc, "ul", "synthesize-manifest-excerpts");
  if (context.manifest.excerpts.length === 0) {
    const item = doc.createElement("li");
    item.textContent = "None.";
    excerpts.appendChild(item);
  } else {
    for (const excerpt of context.manifest.excerpts) {
      const item = doc.createElement("li");
      const code = doc.createElement("code");
      code.textContent = excerptLabel(excerpt.path, excerpt.start_line, excerpt.end_line);
      item.appendChild(code);
      excerpts.appendChild(item);
    }
  }
  view.manifest.replaceChildren(notesHeading, notes, excerptsHeading, excerpts);
  view.packet.textContent = context.markdown;
};

export const setSynthesisFieldError = (target: HTMLElement, message: string): void => {
  target.textContent = message;
  target.hidden = message === "";
};

// A restored candidate carries no rendered HTML, because storing server markup
// and writing it back into the page is not worth the convenience. The Markdown
// is shown as text instead, and the actions still work.
export const renderSynthesisCandidate = (view: SynthesisView, model: SynthesisModel): void => {
  const candidate = model.candidate;
  if (candidate === null) {
    view.candidatePanel.hidden = true;
    view.candidateRendered.replaceChildren();
    view.candidateMarkdown.textContent = "";
    return;
  }
  view.candidatePanel.hidden = false;
  view.candidateMarkdown.textContent = candidate.markdown;
  if (candidate.html !== "") {
    view.candidateRendered.innerHTML = candidate.html;
    return;
  }
  const doc = view.candidateRendered.ownerDocument;
  const note = element(doc, "p", "synthesize-candidate-plain");
  note.textContent = "Restored candidate — shown as Markdown.";
  view.candidateRendered.replaceChildren(note);
};

export const renderSynthesisDestination = (view: SynthesisView, model: SynthesisModel): void => {
  const destination = synthesisDestinationPath(model);
  view.destination.textContent =
    destination === ""
      ? "Enter a title to choose where the note is created."
      : `Creates ${destination}`;
};

export const renderSynthesisPreview = (view: SynthesisView, model: SynthesisModel): void => {
  const preview = model.preview;
  view.applyButton.disabled = preview === null;
  if (preview === null) {
    view.previewPanel.hidden = true;
    view.previewDestination.textContent = "";
    view.previewExpiry.textContent = "";
    view.previewRendered.replaceChildren();
    view.previewMarkdown.textContent = "";
    view.previewSources.replaceChildren();
    return;
  }
  const doc = view.previewSources.ownerDocument;
  view.previewPanel.hidden = false;
  view.previewDestination.textContent = `Creates ${preview.destination}`;
  view.previewExpiry.textContent = `This preview can be applied until ${formatSynthesisExpiry(preview.expires_at)}.`;
  view.previewRendered.innerHTML = preview.rendered_html;
  view.previewMarkdown.textContent = preview.markdown;

  const notes = model.context ? model.context.manifest.notes : [];
  const rows = notes.map((note) => {
    const item = doc.createElement("li");
    const link = doc.createElement("a");
    link.href = note.route;
    link.textContent = note.title || note.path;
    const path = doc.createElement("code");
    path.textContent = note.path;
    item.append(link, " ", path);
    return item;
  });
  if (rows.length === 0) {
    const item = doc.createElement("li");
    item.textContent = "Listed in the note Markdown above.";
    view.previewSources.replaceChildren(item);
    return;
  }
  view.previewSources.replaceChildren(...rows);
};
