import { synthesisDisclosure } from "./synthesize-layout";

import { relatedModeDefinitions } from "./related-model";
import { synthesisPurposeLabel, type SynthesisConfig } from "./synthesize-model";
import {
  element,
  labelled,
  labelledWithError,
  suggestionID,
} from "./synthesize-view-elements";
import type { RelatedMode } from "./related-types";

export const buildSourcesSection = (
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

export const buildSuggestionsSection = (
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

export const buildSearchSection = (
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

export const buildDraftSection = (
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
  heading.textContent = "Draft";

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
  grid.append(folderField.wrapper, labelled(doc, "synthesize-purpose", "Purpose", purpose));
  section.append(
    heading,
    titleField.wrapper,
    draftField.wrapper,
    synthesisDisclosure(doc, "Destination and research options", grid, labelled(doc, "synthesize-prompt", "Prompt", prompt))
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
