import type { TemplateSummary, TemplatesResult } from "./api";
import { queryElement } from "./dom";
import { renderConfigDiff, renderTemplateList } from "./settings-render";
import { setStatusText, type StatusKind } from "./status";
import {
  emptyTemplateDraft,
  templateDirNotice,
  type TemplateDraft,
  type TemplatePendingSave,
} from "./templates-model";

export type TemplatesElements = {
  list: HTMLElement;
  status: HTMLElement;
  name: HTMLInputElement;
  content: HTMLTextAreaElement;
  notice: HTMLElement | null;
  save: HTMLButtonElement | null;
  remove: HTMLButtonElement | null;
  create: HTMLButtonElement | null;
  diff: HTMLElement | null;
  confirm: HTMLButtonElement | null;
  cancel: HTMLButtonElement | null;
  refresh: HTMLButtonElement | null;
};

/**
 * Finds the template panel's elements, or answers null when the page is not the
 * one this controller belongs to.
 *
 * The four required elements are the ones with no sensible fallback: without a
 * list there is nothing to choose from, without the name and content controls
 * there is no draft, and without a status line a refusal would be silent.
 */
export const readTemplatesElements = (root: HTMLElement): TemplatesElements | null => {
  const list = queryElement(root, "[data-templates-list]", HTMLElement);
  const status = queryElement(root, "[data-templates-status]", HTMLElement);
  const name = queryElement(root, "[data-templates-name]", HTMLInputElement);
  const content = queryElement(root, "[data-templates-content]", HTMLTextAreaElement);
  if (!list || !status || !name || !content) {
    return null;
  }
  return {
    list,
    status,
    name,
    content,
    notice: queryElement(root, "[data-templates-notice]", HTMLElement),
    save: queryElement(root, "[data-templates-save]", HTMLButtonElement),
    remove: queryElement(root, "[data-templates-delete]", HTMLButtonElement),
    create: queryElement(root, "[data-templates-new]", HTMLButtonElement),
    diff: queryElement(root, "[data-templates-diff]", HTMLElement),
    confirm: queryElement(root, "[data-templates-confirm]", HTMLButtonElement),
    cancel: queryElement(root, "[data-templates-cancel]", HTMLButtonElement),
    refresh: queryElement(root, "[data-templates-refresh]", HTMLButtonElement),
  };
};

export type TemplatesView = {
  setStatus: (message: string, kind?: StatusKind, uncertain?: boolean) => void;
  showPending: (pending: TemplatePendingSave | null) => void;
  showDraft: (next: TemplateDraft) => void;
  absorb: (result: TemplatesResult) => void;
  draft: () => TemplateDraft;
  extension: () => string;
  /** The bytes currently in the editor, exactly as typed. */
  content: () => string;
  typedName: () => string;
  focusName: () => void;
};

/**
 * Owns everything the template panel renders, and nothing it decides.
 *
 * Keeping the rendered state here is what lets the controller be a list of
 * requests: the draft, the directory listing and the configured extension are
 * all things the DOM already shows, so the controller reads them back instead of
 * keeping a second copy that could disagree with what a person is looking at.
 */
export const createTemplatesView = (elements: TemplatesElements): TemplatesView => {
  let draft = emptyTemplateDraft();
  let listed: TemplateSummary[] = [];
  let extension = "";

  const showDraft = (next: TemplateDraft): void => {
    draft = next;
    elements.name.value = next.name;
    elements.content.value = next.content;
    if (elements.remove) {
      // A delete has to name the bytes it removes, so a draft with no revision —
      // a file that does not exist yet — has nothing to delete.
      elements.remove.disabled = next.revision === "";
    }
    renderTemplateList(elements.list, listed, next.name);
  };

  return {
    setStatus: (message, kind = "", uncertain = false) => {
      setStatusText(elements.status, message, kind);
      elements.status.dataset.writeUnconfirmed = String(uncertain);
    },
    showPending: (pending) => {
      renderConfigDiff(elements.diff, pending ? pending.diff : []);
      // The editor is inert while a receipt is outstanding: the receipt describes
      // bytes the server already planned against, so an edit typed beside the
      // diff is neither what the confirmation is about to write nor a change
      // anything would notice. Clicking another template is the other answer to
      // the same question — that click drops the receipt outright rather than
      // editing past it — so the list stays live.
      elements.content.disabled = pending !== null;
      elements.name.disabled = pending !== null;
      if (elements.confirm) {
        elements.confirm.hidden = pending === null;
        elements.confirm.textContent = pending?.op === "delete" ? "Delete template" : "Save template";
      }
      if (elements.cancel) {
        elements.cancel.hidden = pending === null;
      }
    },
    showDraft,
    absorb: (result) => {
      extension = result.extension;
      listed = result.templates;
      renderTemplateList(elements.list, listed, draft.name);
      if (elements.notice) {
        elements.notice.textContent = templateDirNotice(result.dir, result.templating);
      }
    },
    draft: () => draft,
    extension: () => extension,
    content: () => elements.content.value,
    typedName: () => elements.name.value,
    focusName: () => elements.name.focus(),
  };
};
