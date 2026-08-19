import {
  managementMutationEventName,
  type PropertyMutationRequest,
  type PropertyMutationResult,
  type PropertySchemaDefinitionInput,
  type PropertySchemaOp,
  type PropertyTypedValue,
} from "./api";
import type { FetchLike } from "./properties-model";
import { previewSummaryLabel, type PreviewDialog } from "./properties-preview-dialog";
import type { UndoToast } from "./properties-toast";
import {
  actionConfirmLabel,
  actionTitle,
  mutationSuccessMessage,
  undoToastMessage,
} from "./properties-workbench-model";
import {
  applyPropertyMutation,
  applyPropertySchema,
  previewPropertyMutation,
  previewPropertySchema,
  savePropertyValue,
  undoJournalEntry,
  type MutationOutcome,
} from "./properties-workbench-requests";
import type { StatusKind } from "./status";

export type WorkbenchMutations = {
  previewSchema: (
    op: PropertySchemaOp,
    definition?: PropertySchemaDefinitionInput
  ) => Promise<void>;
  runImmediate: (
    relPath: string,
    op: "replace" | "list_add",
    value: PropertyTypedValue,
    hash: string,
    displayText: string
  ) => Promise<void>;
  runPreview: (request: PropertyMutationRequest) => Promise<void>;
  runUndo: (journalID: string) => Promise<void>;
};

export type WorkbenchMutationOptions = {
  clearSelection: () => void;
  closeEditor: () => void;
  dialog: PreviewDialog;
  doc: Document;
  fetcher: FetchLike;
  historyHref: string;
  property: string;
  reload: () => Promise<void>;
  setBusy: (busy: boolean) => void;
  setStatus: (message: string, kind?: StatusKind) => void;
  toast: UndoToast;
  token: string;
  updateRowText: (relPath: string, text: string) => void;
};

export const createWorkbenchMutations = (
  options: WorkbenchMutationOptions
): WorkbenchMutations => {
  const announce = (
    outcome: MutationOutcome
  ): outcome is { ok: true; result: PropertyMutationResult } => {
    if (!outcome.ok) {
      options.setStatus(outcome.message, "is-error");
      return false;
    }
    return true;
  };

  const finishWrite = async (result: PropertyMutationResult): Promise<void> => {
    options.setStatus(mutationSuccessMessage(result), "is-success");
    options.toast.show({
      message: undoToastMessage(result),
      journalID: result.journal_id,
      historyHref: options.historyHref,
    });
    options.doc.defaultView?.dispatchEvent(
      new CustomEvent(managementMutationEventName, { detail: result })
    );
    await options.reload();
  };

  const runApply = async (previewID: string): Promise<void> => {
    options.setBusy(true);
    options.setStatus("Applying change...", "is-loading");
    try {
      const outcome = await applyPropertyMutation(options.fetcher, options.token, previewID);
      if (!announce(outcome)) {
        return;
      }
      options.clearSelection();
      await finishWrite(outcome.result);
    } finally {
      options.setBusy(false);
    }
  };

  const runPreview = async (request: PropertyMutationRequest): Promise<void> => {
    options.setBusy(true);
    options.setStatus("Previewing change...", "is-loading");
    try {
      const outcome = await previewPropertyMutation(options.fetcher, options.token, request);
      if (!announce(outcome)) {
        return;
      }
      const { result } = outcome;
      options.setStatus(previewSummaryLabel(result));
      options.dialog.open({
        title: actionTitle(request.action, options.property),
        summary: previewSummaryLabel(result),
        confirmLabel: actionConfirmLabel(request.action),
        result,
        onConfirm: () => {
          void runApply(result.preview_id);
        },
        onCancel: () => {
          options.setStatus("Change canceled.");
        },
      });
    } finally {
      options.setBusy(false);
    }
  };

  const runSchemaApply = async (previewID: string): Promise<void> => {
    options.setBusy(true);
    options.setStatus("Saving the definition...", "is-loading");
    try {
      const outcome = await applyPropertySchema(options.fetcher, options.token, previewID);
      if (announce(outcome)) {
        await finishWrite(outcome.result);
      }
    } finally {
      options.setBusy(false);
    }
  };

  const previewSchema = async (
    op: PropertySchemaOp,
    definition?: PropertySchemaDefinitionInput
  ): Promise<void> => {
    options.setBusy(true);
    options.setStatus("Previewing the definition...", "is-loading");
    try {
      const outcome = await previewPropertySchema(options.fetcher, options.token, {
        op,
        property: options.property,
        definition,
      });
      if (!announce(outcome)) {
        return;
      }
      const { result } = outcome;
      options.dialog.open({
        title:
          op === "upsert"
            ? `Define ${options.property}`
            : `Remove the ${options.property} definition`,
        summary: previewSummaryLabel(result),
        confirmLabel: op === "upsert" ? "Save definition" : "Remove definition",
        result,
        onConfirm: () => {
          void runSchemaApply(result.preview_id);
        },
        onCancel: () => {
          options.setStatus("Definition change canceled.");
        },
      });
    } finally {
      options.setBusy(false);
    }
  };

  const runImmediate = async (
    relPath: string,
    op: "replace" | "list_add",
    value: PropertyTypedValue,
    hash: string,
    displayText: string
  ): Promise<void> => {
    options.setBusy(true);
    options.setStatus("Saving...", "is-loading");
    try {
      const outcome = await savePropertyValue(options.fetcher, options.token, {
        note: relPath,
        property: options.property,
        op,
        expected_hash: hash,
        value,
      });
      if (!announce(outcome)) {
        return;
      }
      options.closeEditor();
      options.updateRowText(relPath, displayText);
      await finishWrite(outcome.result);
    } finally {
      options.setBusy(false);
    }
  };

  const runUndo = async (journalID: string): Promise<void> => {
    options.setBusy(true);
    options.setStatus("Undoing change...", "is-loading");
    try {
      const outcome = await undoJournalEntry(options.fetcher, options.token, journalID);
      options.setStatus(outcome.message, outcome.ok ? "is-success" : "is-error");
      if (outcome.ok) {
        options.doc.defaultView?.dispatchEvent(new CustomEvent(managementMutationEventName));
        await options.reload();
      }
    } finally {
      options.setBusy(false);
    }
  };

  return { previewSchema, runImmediate, runPreview, runUndo };
};
