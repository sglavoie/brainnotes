import type { TemplatesResult } from "./api";
import { previewTemplate, applyTemplate, type FetchLike } from "./settings-requests";
import type { StatusKind } from "./status";
import { newWriteOperationID } from "./write-outcome-check";
import {
  templateConfirmMessage,
  templateNoChangeMessage,
  templatePendingSave,
  templateSavedMessage,
  type TemplatePendingSave,
} from "./templates-model";

export type TemplateWriteRequest = {
  op: "save" | "delete";
  name: string;
  content: string;
  /** The digest the bytes were read at; empty means "this file does not exist". */
  revision: string;
};

export type TemplatesWriteHooks = {
  onStatus?: (message: string, kind: StatusKind, uncertain?: boolean, operationID?: string) => void;
  onPending?: (pending: TemplatePendingSave | null) => void;
  /** Fires once the write landed, carrying the receipt it redeemed. */
  onSaved?: (pending: TemplatePendingSave, result: TemplatesResult) => void;
};

export type TemplatesWriter = {
  preview: (request: TemplateWriteRequest) => Promise<void>;
  confirm: () => Promise<void>;
  cancel: () => void;
  pending: () => TemplatePendingSave | null;
  busy: () => boolean;
};

export type TemplatesWriterDeps = {
  fetch: FetchLike;
  token: string;
  hooks?: TemplatesWriteHooks;
};

/**
 * Drives one template write through preview and apply.
 *
 * A create stops on its diff here, unlike a saved view's create: a template is a
 * whole file, so its preview is the only place a person sees the bytes that are
 * about to become one — and a delete could not be auto-chained under any reading,
 * since the preview is the last description of what is being removed.
 *
 * One receipt at a time, redeemed once, dropped on every outcome — the same rule
 * the settings writer states, for the same reason.
 */
export const createTemplatesWriter = (deps: TemplatesWriterDeps): TemplatesWriter => {
  const hooks = deps.hooks || {};
  let pending: TemplatePendingSave | null = null;
  let busy = false;

  const setStatus = (message: string, kind: StatusKind = "", uncertain = false, operationID = ""): void => {
    if (uncertain) hooks.onStatus?.(message, kind, true, operationID);
    else hooks.onStatus?.(message, kind);
  };

  const setPending = (next: TemplatePendingSave | null): void => {
    pending = next;
    hooks.onPending?.(next);
  };

  const preview = async (request: TemplateWriteRequest): Promise<void> => {
    if (busy) {
      return;
    }
    busy = true;
    setPending(null);
    setStatus(`Previewing "${request.name}"...`, "is-loading");
    try {
      const outcome = await previewTemplate(deps.fetch, deps.token, {
        token: deps.token,
        op: request.op,
        name: request.name,
        content: request.op === "delete" ? "" : request.content,
        revision: request.revision,
      });
      if (!outcome.ok) {
        setStatus(outcome.message, "is-error", outcome.uncertain);
        return;
      }
      if (outcome.result.preview_id === "") {
        setStatus(templateNoChangeMessage(request.name), "");
        return;
      }
      // The bytes the server planned against, not the ones the editor holds when
      // the confirmation arrives: the textarea is live until the receipt is
      // redeemed, and the write is pinned to what the diff described.
      const planned = request.op === "delete" ? "" : request.content;
      const issued = templatePendingSave(request.name, request.op, planned, outcome.result);
      setPending(issued);
      setStatus(templateConfirmMessage(issued), "is-warning");
    } finally {
      busy = false;
    }
  };

  const confirm = async (): Promise<void> => {
    const issued = pending;
    if (busy || !issued) {
      setStatus("Preview the change before saving it.", "is-error");
      return;
    }
    busy = true;
    const operationID = newWriteOperationID();
    setStatus(`Saving "${issued.name}"...`, "is-loading");
    try {
      const outcome = await applyTemplate(deps.fetch, deps.token, issued.previewID, operationID);
      setPending(null);
      if (!outcome.ok) {
        setStatus(outcome.message, "is-error", outcome.uncertain, operationID);
        return;
      }
      hooks.onSaved?.(issued, outcome.result);
      setStatus(templateSavedMessage(issued), "is-success");
    } finally {
      busy = false;
    }
  };

  const cancel = (): void => {
    if (pending === null) {
      return;
    }
    setPending(null);
    setStatus("Change cancelled.", "");
  };

  return { preview, confirm, cancel, pending: () => pending, busy: () => busy };
};
