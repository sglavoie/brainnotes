import { queryAllElements, queryElement } from "./dom";
import type { AttachmentElements } from "./source-attachment-panel";
import type { DirtyGateElements } from "./source-dirty-gate";
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
  // vaultID is the server's own name for the vault this note lives in. It is
  // deliberately *not* in the required list below: a page that somehow arrives
  // without it should lose its local drafts, not its editor. See vaultIdentity
  // in internal/web/vault_identity.go for what it is, and source-draft.ts for
  // why a draft key that lacked it could offer one vault's text over another
  // vault's note of the same name.
  vaultID: string;
  maxBytes: number;
  endpoints: SourceEndpoints;
  // attachments and manage are the two surfaces this page grew after the
  // editor itself. Neither is in the required list below, and that is the same
  // decision vaultID gets: a page that cannot name an upload route should lose
  // its attachment panel, not its editor.
  attachments: SourceAttachmentEndpoints;
  manage: SourceManageEndpoints;
  // maxAttachmentBytes is this vault's own per-file limit, which is configurable
  // downward from config.MaxAttachmentFileBytes and so is not a constant the
  // browser may hold.
  maxAttachmentBytes: number;
};

export type SourceAttachmentEndpoints = {
  upload: string;
  cancel: string;
};

// SourceManageEndpoints are the note-management routes the editor page can now
// reach. Every one of them already existed for the note page; `editor` and
// `home` are not routes this page calls but destinations it navigates to, and
// they are published for the same reason as the rest — so the one place a
// destination is built is from a string the server wrote.
export type SourceManageEndpoints = {
  editor: string;
  home: string;
  capture: string;
  create: string;
  rename: string;
  move: string;
  trash: string;
  frontmatterSet: string;
  frontmatterUnset: string;
  pinSet: string;
  pinRemove: string;
  undo: string;
};

// SourceManageElements is the management panel: one control per action, plus the
// gate that stands in front of all of them.
export type SourceManageElements = {
  root: HTMLElement;
  status: HTMLElement | null;
  gate: DirtyGateElements;
  rename: HTMLButtonElement | null;
  renameTitle: HTMLInputElement | null;
  move: HTMLButtonElement | null;
  moveFolder: HTMLInputElement | null;
  trash: HTMLButtonElement | null;
  propertyName: HTMLInputElement | null;
  propertyValue: HTMLInputElement | null;
  propertySet: HTMLButtonElement | null;
  propertyUnset: HTMLButtonElement | null;
  pin: HTMLButtonElement | null;
  unpin: HTMLButtonElement | null;
  capture: HTMLButtonElement | null;
  captureText: HTMLTextAreaElement | null;
  createNote: HTMLButtonElement | null;
  createTarget: HTMLInputElement | null;
  undo: HTMLButtonElement | null;
};

export type SourceEditorElements = {
  panel: HTMLElement;
  surfaceHost: HTMLElement;
  status: HTMLElement | null;
  save: HTMLButtonElement | null;
  find: HTMLButtonElement | null;
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
  // The draft line and the recovery panel. They are listed beside the conflict
  // panel and never merged with it: one is about two versions the vault has
  // seen, the other about text it has never seen at all.
  draftStatus: HTMLElement | null;
  recovery: HTMLElement | null;
  recoveryHeading: HTMLElement | null;
  recoveryLede: HTMLElement | null;
  recoverySource: HTMLElement | null;
  recoveryRestore: HTMLButtonElement | null;
  recoveryRecreate: HTMLButtonElement | null;
  recoveryDiscard: HTMLButtonElement | null;
};

export const readSourceEditorConfig = (panel: HTMLElement): SourceEditorConfig | null => {
  const config: SourceEditorConfig = {
    key: attr(panel, "data-source-key"),
    token: attr(panel, "data-source-csrf"),
    relPath: attr(panel, "data-source-rel-path"),
    vaultID: attr(panel, "data-source-vault-id"),
    maxBytes: Number(attr(panel, "data-source-max-bytes")) || 0,
    endpoints: {
      read: attr(panel, "data-source-read-endpoint"),
      preview: attr(panel, "data-source-preview-endpoint"),
      save: attr(panel, "data-source-save-endpoint"),
      create: attr(panel, "data-source-create-endpoint"),
      search: attr(panel, "data-source-search-endpoint"),
    },
    attachments: {
      upload: attr(panel, "data-source-upload-endpoint"),
      cancel: attr(panel, "data-source-upload-cancel-endpoint"),
    },
    manage: {
      editor: attr(panel, "data-source-editor-path"),
      home: attr(panel, "data-source-home-path"),
      capture: attr(panel, "data-source-capture-endpoint"),
      create: attr(panel, "data-source-note-create-endpoint"),
      rename: attr(panel, "data-source-rename-endpoint"),
      move: attr(panel, "data-source-move-endpoint"),
      trash: attr(panel, "data-source-trash-endpoint"),
      frontmatterSet: attr(panel, "data-source-frontmatter-set-endpoint"),
      frontmatterUnset: attr(panel, "data-source-frontmatter-unset-endpoint"),
      pinSet: attr(panel, "data-source-pin-set-endpoint"),
      pinRemove: attr(panel, "data-source-pin-remove-endpoint"),
      undo: attr(panel, "data-source-undo-endpoint"),
    },
    maxAttachmentBytes: Number(attr(panel, "data-source-max-attachment-bytes")) || 0,
  };

  const required = [
    config.key,
    config.token,
    config.endpoints.read,
    config.endpoints.preview,
    config.endpoints.save,
    config.endpoints.create,
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
    find: queryElement(panel, "[data-source-find]", HTMLButtonElement),
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
    draftStatus: queryElement(panel, "[data-source-draft-status]", HTMLElement),
    recovery: queryElement(panel, "[data-source-recovery]", HTMLElement),
    recoveryHeading: queryElement(panel, "[data-source-recovery-heading]", HTMLElement),
    recoveryLede: queryElement(panel, "[data-source-recovery-lede]", HTMLElement),
    recoverySource: queryElement(panel, "[data-source-recovery-source]", HTMLElement),
    recoveryRestore: queryElement(panel, "[data-source-recovery-restore]", HTMLButtonElement),
    recoveryRecreate: queryElement(panel, "[data-source-recovery-recreate]", HTMLButtonElement),
    recoveryDiscard: queryElement(panel, "[data-source-recovery-discard]", HTMLButtonElement),
  };
};

// readAttachmentElements answers the panel's parts and its status line. The drop zone is
// the editing surface rather than the panel: a file is dropped where the text
// is, and a drop target that was the attachment list would mean aiming at a box
// that is hidden until something is already in it.
export const readAttachmentElements = (panel: HTMLElement): AttachmentElements => ({
  panel: queryElement(panel, "[data-source-attachments]", HTMLElement),
  input: queryElement(panel, "[data-source-attachment-input]", HTMLInputElement),
  choose: queryElement(panel, "[data-source-attachment-choose]", HTMLButtonElement),
  list: queryElement(panel, "[data-source-attachment-list]", HTMLElement),
  dropZone: queryElement(panel, "[data-source-surface]", HTMLElement),
  status: queryElement(panel, "[data-source-attachment-status]", HTMLElement),
});

export const readSourceManageElements = (panel: HTMLElement): SourceManageElements | null => {
  const root = queryElement(panel, "[data-source-manage]", HTMLElement);
  if (!root) {
    return null;
  }
  return {
    root,
    status: queryElement(panel, "[data-source-manage-status]", HTMLElement),
    gate: {
      panel: queryElement(panel, "[data-source-gate]", HTMLElement),
      message: queryElement(panel, "[data-source-gate-message]", HTMLElement),
      save: queryElement(panel, "[data-source-gate-save]", HTMLButtonElement),
      proceed: queryElement(panel, "[data-source-gate-proceed]", HTMLButtonElement),
      cancel: queryElement(panel, "[data-source-gate-cancel]", HTMLButtonElement),
    },
    rename: queryElement(panel, "[data-source-rename]", HTMLButtonElement),
    renameTitle: queryElement(panel, "[data-source-rename-title]", HTMLInputElement),
    move: queryElement(root, "[data-source-move]", HTMLButtonElement),
    moveFolder: queryElement(root, "[data-source-move-folder]", HTMLInputElement),
    trash: queryElement(root, "[data-source-trash]", HTMLButtonElement),
    propertyName: queryElement(root, "[data-source-property-name]", HTMLInputElement),
    propertyValue: queryElement(root, "[data-source-property-value]", HTMLInputElement),
    propertySet: queryElement(root, "[data-source-property-set]", HTMLButtonElement),
    propertyUnset: queryElement(root, "[data-source-property-unset]", HTMLButtonElement),
    pin: queryElement(root, "[data-source-pin]", HTMLButtonElement),
    unpin: queryElement(root, "[data-source-unpin]", HTMLButtonElement),
    capture: queryElement(root, "[data-source-capture]", HTMLButtonElement),
    captureText: queryElement(root, "[data-source-capture-text]", HTMLTextAreaElement),
    createNote: queryElement(root, "[data-source-note-create]", HTMLButtonElement),
    createTarget: queryElement(root, "[data-source-note-create-target]", HTMLInputElement),
    undo: queryElement(root, "[data-source-undo]", HTMLButtonElement),
  };
};

const attr = (element: HTMLElement, name: string): string =>
  (element.getAttribute(name) || "").trim();
