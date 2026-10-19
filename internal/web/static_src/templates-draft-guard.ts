import { confirmAction } from "./action-confirm";
import { fetchTemplate, type FetchLike } from "./settings-requests";
import type { TemplatesWriter } from "./templates-actions";
import { emptyTemplateDraft, templateDraftOf, type TemplatePendingSave } from "./templates-model";
import type { TemplatesView } from "./templates-view";

export const DISCARD_TEMPLATE_EDITS =
  "Discard the unsaved changes to this template? The editor will show the template you chose instead.";

export type TemplateConfirm = (message: string) => boolean | Promise<boolean>;

export type TemplateDraftGuard = {
  /** Runs `next` once the editor may be replaced, asking first if it holds edits. */
  replace: (next: () => void) => void;
  open: (name: string) => Promise<void>;
  /** Supersedes any open still in flight, for a draft started from scratch. */
  supersede: () => void;
  /** Refuses a write while an open is in flight, saying why. */
  refuseWhileOpening: () => boolean;
  settleConfirmed: (issued: TemplatePendingSave) => Promise<void>;
};

type GuardDeps = {
  root: HTMLElement;
  fetch: FetchLike;
  view: TemplatesView;
  writer: () => TemplatesWriter;
  confirm?: TemplateConfirm;
  reload: () => void;
};

/**
 * Opens a template into the draft, reading its current bytes and revision.
 * `current` reports whether this is still the latest open asked for, so a slow
 * read answered after a later click never replaces the draft that click chose.
 */
const openInto = async (
  fetcher: FetchLike,
  view: TemplatesView,
  name: string,
  current: () => boolean
): Promise<void> => {
  view.setStatus(`Opening "${name}"…`, "is-loading");
  const outcome = await fetchTemplate(fetcher, name);
  if (!current()) {
    return;
  }
  if (!outcome.ok) {
    view.setStatus(outcome.message, "is-error");
    return;
  }
  const summary = outcome.result.templates[0];
  if (!summary) {
    view.setStatus(`"${name}" is no longer in the templates directory.`, "is-error");
    return;
  }
  view.showDraft(templateDraftOf(summary));
  view.setStatus(`Editing ${outcome.result.rel_path || name}.`, "");
};

/**
 * Decides when the editor's draft may be replaced.
 *
 * Each open takes a number; only the latest one may settle the draft, and a
 * write waits until it has, so a receipt never pairs with bytes it did not see.
 */
export const createTemplateDraftGuard = (deps: GuardDeps): TemplateDraftGuard => {
  const { view } = deps;
  const confirm = deps.confirm || ((message: string) => confirmAction({
    title: "Discard template edits?",
    description: message,
    confirmLabel: "Discard edits",
    danger: true,
  }, deps.root.ownerDocument));
  let opening = 0;
  let openPending = false;

  // A pending receipt holds typed bytes no write has stored yet, so it counts
  // as an edit too.
  const modified = (): boolean => {
    const draft = view.draft();
    return deps.writer().pending() !== null || view.content() !== draft.content || view.typedName() !== draft.name;
  };

  const replace = (next: () => void): void => {
    const writer = deps.writer();
    if (writer.busy()) return;
    const proceed = (): void => {
      // The confirmation can outlast a write started from another control.
      if (writer.busy()) return;
      writer.cancel();
      next();
    };
    if (!modified()) {
      proceed();
      return;
    }
    const answer = confirm(DISCARD_TEMPLATE_EDITS);
    if (typeof answer === "boolean") {
      if (answer) proceed();
      return;
    }
    void answer.then((accepted) => {
      if (accepted) proceed();
    });
  };

  const open = async (name: string): Promise<void> => {
    const issue = ++opening;
    openPending = true;
    try {
      await openInto(deps.fetch, view, name, () => issue === opening);
    } finally {
      if (issue === opening) openPending = false;
    }
  };

  // A confirmed outcome is the write that went unanswered. Its revision was
  // never received, so the template is read again; the editor takes it only if
  // it still holds the bytes that write carried.
  const settleConfirmed = async (issued: TemplatePendingSave): Promise<void> => {
    deps.reload();
    if (view.typedName() !== issued.name) return;
    if (issued.op === "delete") {
      view.showDraft(emptyTemplateDraft());
      return;
    }
    if (view.content() !== issued.content) return;
    const issue = ++opening;
    const outcome = await fetchTemplate(deps.fetch, issued.name);
    const summary = outcome.ok ? outcome.result.templates[0] : undefined;
    if (issue === opening && summary && view.content() === issued.content) {
      view.showDraft(templateDraftOf(summary));
    }
  };

  return {
    replace,
    open,
    supersede: () => {
      opening += 1;
      openPending = false;
    },
    refuseWhileOpening: () => {
      if (!openPending) return false;
      view.setStatus("Wait for the template to finish opening.", "is-error");
      return true;
    },
    settleConfirmed,
  };
};
