import { confirmAction } from "./action-confirm";
import { withLibraryContext } from "./note-navigation";
import { normalizeSearchResults } from "./api";
import { queryElement } from "./dom";
import { setSourceStatus } from "./source-status";
import { setStatusText } from "./status";
import {
  createSourceEditorController,
  type SourceEditorController,
} from "./source-editor-controller";
import {
  readAttachmentElements,
  readSourceEditorConfig,
  readSourceEditorElements,
  readSourceManageElements,
  type SourceEditorConfig,
  type SourceEditorElements,
} from "./source-editor-elements";
import type { SourceDraftStore } from "./source-draft";
import { createSourceSurfaceFactory } from "./source-editor-view";
import {
  createSourceDraftStore,
  resolveDraftStorage,
  resolveDraftTabID,
} from "./source-draft";
import {
  createAttachmentTracker,
  type AttachmentTracker,
} from "./source-attachments";
import { closeDisclosureOnEscape } from "./disclosure-popover";
import { initAttachmentPanel, renderAttachmentList } from "./source-attachment-panel";
import { initSourceManage } from "./source-editor-manage";
import { createSourceClient, randomID, type AttachmentClaims } from "./source-save";
import { wikilinkOptions, wikilinkSearchURL } from "./source-wikilink";

// The editor's entry point: find the page's panel, build the pieces, load the
// note.
//
// It is the editor module editing.ts knows about, and it does no work of its own
// beyond assembly. A page without `[data-source-editor]` — which is every page
// but /edit — returns immediately. The editing bundle is also used by /sync.

// createDraftStore builds the browser-local copy of unsaved text, or nothing.
//
// It returns null when the page did not publish a vault identity, and that is a
// refusal rather than a fallback: a draft key without it would be
// `note key + tab`, which is the same key for a note called `Alpha.md` in every
// vault this origin has ever served. Losing the drafts for a page that cannot
// name its vault costs unsaved text nobody has typed yet; guessing costs
// somebody the wrong note's words.
const createDraftStore = (
  config: SourceEditorConfig,
  tabID: string
): SourceDraftStore | null =>
  config.vaultID === ""
    ? null
    : createSourceDraftStore({
        vaultID: config.vaultID,
        noteKey: config.key,
        draftID: tabID,
        storage: resolveDraftStorage(),
      });

export const initSourceEditor = (): void => {
  const panel = queryElement(document, "[data-source-editor]", HTMLElement);
  if (!panel) {
    return;
  }
  const config = readSourceEditorConfig(panel);
  const elements = readSourceEditorElements(panel);
  if (!config || !elements) {
    return;
  }

  const client = createSourceClient({
    endpoints: config.endpoints,
    key: config.key,
    token: config.token,
  });

  // The surface, the tracker and the management panel each need the controller,
  // and the controller needs the first of them. They close over a mutable
  // reference rather than taking it, because none of them can be used before the
  // editor they live in exists: a key cannot be pressed, a file cannot finish
  // uploading, and a button cannot be clicked.
  let controller: SourceEditorController | null = null;
  const createSurface = createSourceSurfaceFactory({
    onSave: () => void controller?.save(),
    onCommand: (command, options) => controller?.runCommand(command, options),
    completeWikilink: async (query) => {
      const payload = await client.search(
        wikilinkSearchURL(config.endpoints.search, query)
      );
      return wikilinkOptions(normalizeSearchResults(payload));
    },
  });

  // One tab id, used twice. The draft store files this tab's kept text under it
  // and the upload owner is the same string, which is what makes an upload
  // belong to the draft that staged it: a second tab editing the same note gets
  // its own id, so neither can commit the other's staged files.
  const tabID = resolveDraftTabID(() => randomID("tab"));
  const tracker = initAttachments(config, elements, tabID, () => controller);

  controller = createSourceEditorController({
    elements,
    config,
    client,
    createSurface,
    drafts: createDraftStore(config, tabID),
    claims: attachmentClaims(tracker, tabID),
  });
  initManagement(panel, config, () => controller);
  initFormatDisclosure(panel);
  void controller.load();
};

// initAttachments builds the upload tracker and wires the panel around it, or
// answers null for a page that published no upload route — a server that cannot
// accept attachments, which is every read-only replica.
const initAttachments = (
  config: SourceEditorConfig,
  elements: SourceEditorElements,
  owner: string,
  current: () => SourceEditorController | null
): AttachmentTracker | null => {
  if (config.attachments.upload === "" || config.attachments.cancel === "") {
    return null;
  }
  const attachmentElements = readAttachmentElements(elements.panel);
  const tracker = createAttachmentTracker({
    owner,
    token: config.token,
    uploadEndpoint: config.attachments.upload,
    cancelEndpoint: config.attachments.cancel,
    maxBytes: config.maxAttachmentBytes,
    newID: () => randomID("attachment"),
    host: {
      insertLink: (markdown) => current()?.insertText(markdown),
      setStatus: (message, kind) =>
        attachmentElements.status
          ? setStatusText(attachmentElements.status, message, kind)
          : setSourceStatus(elements.status, message, kind),
      changed: () =>
        renderAttachmentList(attachmentElements.list, tracker.entries(), document),
    },
  });
  initAttachmentPanel({ elements: attachmentElements, tracker, doc: document });
  return tracker;
};

// attachmentClaims turns the tracker into the two questions a write asks it. The
// wire names an upload `upload_id`; the tracker calls it `uploadID`, and this is
// the one place the two spellings meet.
const attachmentClaims = (
  tracker: AttachmentTracker | null,
  owner: string
): AttachmentClaims | null =>
  tracker === null
    ? null
    : {
        owner,
        pending: () => tracker.pendingIDs(),
        committed: (saved) =>
          tracker.settle(
            saved.map((item) => ({ uploadID: item.upload_id, relPath: item.rel_path }))
          ),
      };

// initManagement wires the note's own management controls, for a page that has
// them. A page without the panel — or a build where the template has not grown
// it — simply has no management, exactly as it did before.
const initManagement = (
  panel: HTMLElement,
  config: SourceEditorConfig,
  current: () => SourceEditorController | null
): void => {
  const elements = readSourceManageElements(panel);
  if (!elements) {
    return;
  }
  initSourceManage({
    elements,
    config,
    host: {
      key: () => config.key,
      dirty: () => current()?.dirty() ?? false,
      reload: async () => {
        await current()?.load();
      },
      navigate: (url) => window.location.assign(withLibraryContext(url, true)),
      save: async () => {
        await current()?.save();
      },
      confirm: (message) => confirmAction({ title: "Move note to Trash?", description: message, confirmLabel: "Move to Trash" }, panel.ownerDocument, elements.trash),
    },
  });
};

const initFormatDisclosure = (panel: HTMLElement): void => {
  const details = panel.querySelector<HTMLDetailsElement>(".source-editor-format");
  if (!details) return;
  details.addEventListener("click", (event) => {
    if (event.target instanceof Element && event.target.closest("[data-source-command]")) details.open = false;
  });
  closeDisclosureOnEscape(details);
};
