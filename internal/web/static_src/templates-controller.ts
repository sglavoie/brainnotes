import type { TemplatesResult } from "./api";
import { fetchTemplates, type FetchLike } from "./settings-requests";
import type { StatusKind } from "./status";
import { createTemplatesWriter } from "./templates-actions";
import {
  emptyTemplateDraft,
  newTemplateDraft,
  suggestTemplateName,
  templateSaveError,
  type TemplateDraft,
  type TemplatePendingSave,
} from "./templates-model";
import { createTemplateDraftGuard, type TemplateConfirm } from "./templates-draft-guard";
import { createTemplatesView, readTemplatesElements, type TemplatesView } from "./templates-view";
import { offerWriteOutcomeCheck } from "./write-outcome-check";

export type TemplatesPanelDeps = {
  root: HTMLElement;
  fetch: FetchLike;
  token: string;
  confirm?: TemplateConfirm;
};

export type TemplatesPanel = {
  load: () => Promise<void>;
  draft: () => TemplateDraft;
  pending: () => TemplatePendingSave | null;
};

const listSummary = (result: TemplatesResult): [string, StatusKind] =>
  result.templates.length === 0
    ? [
        `No templates yet. Name one and save it into ${result.dir || "the templates directory"}.`,
        "is-empty",
      ]
    : [`${result.templates.length} templates in ${result.dir}/.`, ""];

/** Reads the name a click on the list is asking for, or "" for a click on nothing. */
const clickedTemplate = (event: Event): string => {
  const target = event.target;
  if (!(target instanceof HTMLElement)) {
    return "";
  }
  return target.closest("[data-templates-open]")?.getAttribute("data-templates-open") || "";
};

/**
 * Re-reads the directory listing into the view, or reports why it could not.
 *
 * The listing is reloaded after every write rather than patched in place: a save
 * can change a name, a delete removes one, and the server is the only thing that
 * knows which files the templates directory now holds.
 */
const reloadInto = async (
  fetcher: FetchLike,
  view: TemplatesView
): Promise<TemplatesResult | null> => {
  const outcome = await fetchTemplates(fetcher);
  if (!outcome.ok) {
    view.setStatus(outcome.message, "is-error");
    return null;
  }
  view.absorb(outcome.result);
  return outcome.result;
};

// The draft is settled on the bytes the receipt carried — the ones the server
// was asked to write and the diff described — rather than on whatever the
// textarea holds now. They are the same thing for a person who confirmed what
// they typed, and different for one who kept typing over the diff: settling on
// the editor would leave unwritten bytes on screen under a "Saved" message.
const savedSettler = (fetcher: FetchLike, view: TemplatesView) =>
  (issued: TemplatePendingSave, result: TemplatesResult): void => {
    view.showDraft(
      issued.op === "delete"
        ? emptyTemplateDraft()
        : { name: issued.name, content: issued.content, revision: result.revision, created: false }
    );
    void reloadInto(fetcher, view);
  };

/**
 * Mounts the template editor.
 *
 * The draft's revision is the load-bearing field: it is the digest the bytes in
 * the textarea were read at, it travels on every write, and it is what makes a
 * template changed underneath this page — in another window, by `bn`, or by an
 * arriving synchronization package — a refusal rather than an overwrite. A name
 * typed over an opened draft deliberately drops it: that edit is a create, and
 * the server refuses a create whose destination exists, which is the answer a
 * person wants rather than a silent overwrite of a second file.
 *
 * Nothing here parses a template. The bytes round-trip exactly, because a
 * template's frontmatter holds unrendered actions no YAML parser accepts — the
 * same reason the vault walker prunes this directory.
 */
export const createTemplatesPanel = (deps: TemplatesPanelDeps): TemplatesPanel | null => {
  const elements = readTemplatesElements(deps.root);
  if (!elements) {
    return null;
  }
  const view = createTemplatesView(elements);
  const guard = createTemplateDraftGuard({
    root: deps.root, fetch: deps.fetch, view, writer: () => writer, confirm: deps.confirm,
    reload: () => void reloadInto(deps.fetch, view),
  });

  const settle = savedSettler(deps.fetch, view);

  let issued: TemplatePendingSave | null = null;
  const writer = createTemplatesWriter({
    fetch: deps.fetch,
    token: deps.token,
    hooks: {
      onStatus: (message, kind, uncertain, operationID) => {
        view.setStatus(message, kind, uncertain);
        const unanswered = issued;
        if (uncertain && operationID && unanswered) {
          offerWriteOutcomeCheck(elements.status, operationID, deps.fetch, "Template change",
            () => void guard.settleConfirmed(unanswered));
        }
      },
      onPending: (pending) => {
        if (pending) issued = pending;
        view.showPending(pending);
      },
      onSaved: settle,
    },
  });

  const requestSave = (): void => {
    if (guard.refuseWhileOpening()) return;
    const draft = view.draft();
    const name = suggestTemplateName(view.typedName(), view.extension());
    const refusal = templateSaveError({ ...draft, name });
    if (refusal !== "") {
      view.showPending(null);
      view.setStatus(refusal, "is-error");
      return;
    }
    void writer.preview({
      op: "save",
      name,
      content: view.content(),
      // A name typed over an opened draft is a different file, so the revision
      // the draft was read at no longer describes the destination.
      revision: name === draft.name ? draft.revision : "",
    });
  };

  const requestDelete = (): void => {
    if (guard.refuseWhileOpening()) return;
    const draft = view.draft();
    if (draft.revision === "") {
      view.setStatus("Open a template before deleting it.", "is-error");
      return;
    }
    void writer.preview({ op: "delete", name: draft.name, content: "", revision: draft.revision });
  };

  const load = async (): Promise<void> => {
    view.showPending(null);
    view.setStatus("Loading templates...", "is-loading");
    const result = await reloadInto(deps.fetch, view);
    if (result) {
      view.setStatus(...listSummary(result));
    }
  };

  elements.list.addEventListener("click", (event) => {
    const name = clickedTemplate(event);
    // A click during a write would open a draft the pending receipt no longer
    // describes, so the list is inert until that write settles.
    if (name !== "") {
      guard.replace(() => void guard.open(name));
    }
  });
  elements.save?.addEventListener("click", requestSave);
  elements.remove?.addEventListener("click", requestDelete);
  elements.create?.addEventListener("click", () => guard.replace(() => {
    guard.supersede();
    view.showDraft(newTemplateDraft("", ""));
    view.setStatus("Name the new template and save it.", "");
    view.focusName();
  }));
  elements.confirm?.addEventListener("click", () => void writer.confirm());
  elements.cancel?.addEventListener("click", () => writer.cancel());
  elements.refresh?.addEventListener("click", () => void load());
  view.showPending(null);
  view.showDraft(emptyTemplateDraft());

  return { load, draft: view.draft, pending: () => writer.pending() };
};
