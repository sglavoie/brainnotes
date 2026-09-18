import { queryAllElements, queryElement } from "./dom";
import type { SourceEndpoints } from "./source-save";

// Reading the editor page's own description of itself.
//
// Every endpoint, the note key and the CSRF token are data attributes the Go
// template wrote, rather than constants duplicated here — the server owns those
// route strings and `internal/web/editor_page_test.go` pins that it publishes
// them. A page missing any of them yields a null config, and the editor simply
// does not start: a half-configured editor that posts to "" is worse than an
// inert textarea.

export type SourceEditorConfig = {
  key: string;
  token: string;
  relPath: string;
  maxBytes: number;
  endpoints: SourceEndpoints;
};

export type SourceEditorElements = {
  panel: HTMLElement;
  surfaceHost: HTMLElement;
  status: HTMLElement | null;
  save: HTMLButtonElement | null;
  commands: HTMLButtonElement[];
  tabs: HTMLButtonElement[];
  panes: HTMLElement[];
  // paneContainer is the element the pane selection is published on, and it is
  // read from the template's own marker rather than derived from a pane's
  // parent. css/responsive.css selects `.source-editor-panes[data-source-active]`
  // — one element by name — so deriving it would let a wrapper element inserted
  // between the two put the attribute somewhere no stylesheet looks, with every
  // jsdom assertion still passing because they ask the same derivation.
  paneContainer: HTMLElement;
  preview: HTMLElement | null;
  previewStatus: HTMLElement | null;
  conflict: HTMLElement | null;
  conflictSource: HTMLElement | null;
  conflictOverwrite: HTMLButtonElement | null;
  conflictDiscard: HTMLButtonElement | null;
};

export const readSourceEditorConfig = (panel: HTMLElement): SourceEditorConfig | null => {
  const config: SourceEditorConfig = {
    key: attr(panel, "data-source-key"),
    token: attr(panel, "data-source-csrf"),
    relPath: attr(panel, "data-source-rel-path"),
    maxBytes: Number(attr(panel, "data-source-max-bytes")) || 0,
    endpoints: {
      read: attr(panel, "data-source-read-endpoint"),
      preview: attr(panel, "data-source-preview-endpoint"),
      save: attr(panel, "data-source-save-endpoint"),
      search: attr(panel, "data-source-search-endpoint"),
    },
  };

  const required = [
    config.key,
    config.token,
    config.endpoints.read,
    config.endpoints.preview,
    config.endpoints.save,
    config.endpoints.search,
  ];
  return required.every((value) => value !== "") ? config : null;
};

export const readSourceEditorElements = (panel: HTMLElement): SourceEditorElements | null => {
  const surfaceHost = queryElement(panel, "[data-source-surface]", HTMLElement);
  if (!surfaceHost) {
    return null;
  }
  const panes = queryAllElements(panel, "[data-source-pane]", HTMLElement);
  return {
    panel,
    surfaceHost,
    status: queryElement(panel, "[data-source-status]", HTMLElement),
    save: queryElement(panel, "[data-source-save]", HTMLButtonElement),
    commands: queryAllElements(panel, "[data-source-command]", HTMLButtonElement),
    tabs: queryAllElements(panel, "[data-source-tab]", HTMLButtonElement),
    panes,
    paneContainer:
      queryElement(panel, "[data-source-panes]", HTMLElement) ??
      panes[0]?.parentElement ??
      panel,
    preview: queryElement(panel, "[data-source-preview]", HTMLElement),
    previewStatus: queryElement(panel, "[data-source-preview-status]", HTMLElement),
    conflict: queryElement(panel, "[data-source-conflict]", HTMLElement),
    conflictSource: queryElement(panel, "[data-source-conflict-source]", HTMLElement),
    conflictOverwrite: queryElement(panel, "[data-source-conflict-overwrite]", HTMLButtonElement),
    conflictDiscard: queryElement(panel, "[data-source-conflict-discard]", HTMLButtonElement),
  };
};

const attr = (element: HTMLElement, name: string): string =>
  (element.getAttribute(name) || "").trim();
