import { responseFailure, unansweredRequest } from "./request-outcome";
import {
  managementMutationEventName,
  normalizeBulkManagementResult,
  normalizeBulkPreviewManagementResult,
  normalizeManagementErrorMessage,
  type BulkAction,
  type BulkPreviewManagementResult,
} from "./api";
import { queryAllElements } from "./dom";
import type { OverviewSearchElements } from "./overview-search-elements";
import {
  bulkApplyFallbackMessage,
  bulkStatusMessage,
  previewFingerprint,
  readMoveFields,
  readSetFields,
  readUnsetFields,
  type BulkActionFields,
  type BulkPreviewState,
  type BulkStatusKind,
  type OverviewSearchDeps,
} from "./overview-search-model";
import {
  selectedOverviewKeys,
  syncOverviewActionButton,
} from "./overview-search-controller";
import { renderPreviewPanel, syncPreviewAttributes } from "./overview-search-render";
import { postBulkApply, postBulkPreview } from "./overview-search-requests";
import { setStatusText } from "./status";
import { syncOverviewSelectionControls } from "./overview-search-selection";
import { newWriteOperationID, offerWriteOutcomeCheck } from "./write-outcome-check";

// The bulk-action half of the overview search: selection-driven preview/apply
// state and its controls. OverviewSearch extends it with the search half.
export abstract class OverviewSearchBulk {
  protected readonly fetcher: typeof fetch;
  protected readonly selectedKeys = new Map<string, string>();
  protected bulkBusy = false;
  protected previewState: BulkPreviewState | null = null;
  protected ignoreNextManagementMutationClear = false;
  protected abstract scheduleSearch: (delay: number) => void;
  protected selectionChanged(): void {}

  constructor(protected readonly els: OverviewSearchElements, deps: OverviewSearchDeps) {
    this.fetcher = deps.fetch || ((input, init) => fetch(input, init));
  }

  protected setBulkStatus = (message: string, kind: BulkStatusKind = ""): void => {
    setStatusText(this.els.bulkStatusEl, message || " ", kind, [
      "is-success",
      "is-error",
      "is-loading",
    ]);
  };

  protected previewMatches = (
    action: BulkAction,
    fields: BulkActionFields,
    keys = selectedOverviewKeys(this.selectedKeys)
  ): boolean =>
    Boolean(
      this.previewState &&
      this.previewState.previewID &&
      this.previewState.fingerprint === previewFingerprint(action, fields, keys)
    );

  protected readFieldsForAction = (action: BulkAction): BulkActionFields => {
    switch (action) {
      case "move":
        return readMoveFields(this.els.moveForm);
      case "frontmatter_set":
        return readSetFields(this.els.setForm);
      case "frontmatter_unset":
        return readUnsetFields(this.els.unsetForm);
      default:
        return {};
    }
  };

  protected disableBulkControls = (root: HTMLElement, disabled: boolean): void => {
    for (const control of queryAllElements(root, "button, input, select", HTMLElement)) {
      if (
        control instanceof HTMLButtonElement ||
        control instanceof HTMLInputElement ||
        control instanceof HTMLSelectElement
      ) {
        control.disabled = disabled;
      }
    }
  };

  protected selectionBusy = (): boolean =>
    this.bulkBusy || this.els.root.getAttribute("aria-busy") === "true";

  protected updateBulkControls = (): void => {
    this.selectionChanged();
    syncOverviewSelectionControls(this.els.root, this.els.listEl, this.selectedKeys.size,
      this.selectionBusy(), this.selectedKeys);
    if (!this.els.bulkRoot) {
      return;
    }
    const count = this.selectedKeys.size;
    this.els.bulkRoot.hidden = count === 0;
    this.disableBulkControls(this.els.bulkRoot, this.selectionBusy() || count === 0);
    syncOverviewActionButton(this.els.trashButton, "trash", this.previewMatches("trash", {}));
    syncOverviewActionButton(this.els.moveButton, "move", this.previewMatches("move", readMoveFields(this.els.moveForm)));
    syncOverviewActionButton(this.els.setButton, "frontmatter_set", this.previewMatches("frontmatter_set", readSetFields(this.els.setForm)));
    syncOverviewActionButton(this.els.unsetButton, "frontmatter_unset", this.previewMatches("frontmatter_unset", readUnsetFields(this.els.unsetForm)));
    if (this.els.previewApplyButton) {
      this.els.previewApplyButton.disabled = this.selectionBusy() || count === 0 || this.previewState === null;
    }
    if (this.els.previewClearButton) {
      this.els.previewClearButton.disabled = this.bulkBusy || this.previewState === null;
    }
    renderPreviewPanel(this.els, this.previewState);
  };

  protected clearPreviewState = ({ clearStatus = false }: { clearStatus?: boolean } = {}): void => {
    const hadPreview = this.previewState !== null;
    this.previewState = null;
    syncPreviewAttributes(this.els.bulkRoot, this.previewState);
    this.updateBulkControls();
    if (clearStatus && hadPreview) {
      this.setBulkStatus("");
    }
  };

  protected setPreviewState = (
    action: BulkAction,
    fields: BulkActionFields,
    result: BulkPreviewManagementResult
  ): void => {
    this.previewState = {
      action,
      fingerprint: previewFingerprint(action, fields, selectedOverviewKeys(this.selectedKeys)),
      previewID: result.preview_id || "",
      expiresAt: result.expires_at,
      result,
    };
    syncPreviewAttributes(this.els.bulkRoot, this.previewState);
    this.updateBulkControls();
  };

  protected setBulkBusy = (busy: boolean): void => {
    this.bulkBusy = busy;
    this.updateBulkControls();
  };

  protected canRunBulk = (keys: string[]): boolean => {
    if (!this.els.bulkRoot || this.els.csrfToken.trim() === "") {
      this.setBulkStatus("Bulk action unavailable.", "is-error");
      return false;
    }
    if (keys.length === 0) {
      this.setBulkStatus("Select one or more notes.", "is-error");
      return false;
    }
    return true;
  };

  protected runBulkPreview = async (
    action: BulkAction,
    fields: BulkActionFields,
    fallbackMessage: string
  ): Promise<void> => {
    const keys = selectedOverviewKeys(this.selectedKeys);
    if (!this.canRunBulk(keys)) {
      return;
    }

    this.setBulkBusy(true);
    this.setBulkStatus("Previewing bulk action...", "is-loading");
    try {
      const { resp, payload } = await postBulkPreview(
        this.fetcher,
        this.els.csrfToken,
        action,
        keys,
        fields
      );
      if (!resp.ok) {
        this.clearPreviewState();
        this.setBulkStatus(normalizeManagementErrorMessage(resp.status, payload), "is-error");
        return;
      }
      const result = normalizeBulkPreviewManagementResult(payload);
      if (!result.ok || !result.preview_id) {
        this.clearPreviewState();
        this.setBulkStatus(result.message || "Bulk preview failed.", "is-error");
        return;
      }
      this.setPreviewState(action, fields, result);
      const failures = Array.isArray(result.failures) ? result.failures : [];
      this.setBulkStatus(
        bulkStatusMessage(result, fallbackMessage),
        failures.length > 0 ? "is-error" : "is-success"
      );
    } catch {
      this.clearPreviewState();
      this.setBulkStatus("Bulk preview failed.", "is-error");
    } finally {
      this.setBulkBusy(false);
    }
  };

  private reportUnconfirmedBulkApply = (message: string, operationID: string): void => {
    this.clearPreviewState();
    this.setBulkStatus(message, "is-error");
    if (this.els.bulkStatusEl) {
      offerWriteOutcomeCheck(this.els.bulkStatusEl, operationID, this.fetcher, "Bulk change");
    }
  };

  protected runBulkApply = async (
    action: BulkAction,
    fields: BulkActionFields,
    fallbackMessage: string
  ): Promise<void> => {
    const keys = selectedOverviewKeys(this.selectedKeys);
    if (!this.canRunBulk(keys)) {
      return;
    }
    if (!this.previewState || !this.previewMatches(action, fields, keys)) {
      this.clearPreviewState();
      this.setBulkStatus("Preview the bulk action before applying it.", "is-error");
      return;
    }

    this.setBulkBusy(true);
    const operationID = newWriteOperationID();
    this.setBulkStatus("Applying bulk action...", "is-loading");
    try {
      const { resp, payload } = await postBulkApply(
        this.fetcher,
        this.els.csrfToken,
        action,
        keys,
        fields,
        this.previewState.previewID,
        operationID
      );
      const failure = responseFailure(resp, payload, "write");
      if (failure?.uncertain) {
        this.reportUnconfirmedBulkApply(failure.message, operationID);
        return;
      }
      if (!resp.ok) {
        this.clearPreviewState();
        this.setBulkStatus(normalizeManagementErrorMessage(resp.status, payload), "is-error");
        return;
      }
      const result = normalizeBulkManagementResult(payload);
      const failures = Array.isArray(result.failures) ? result.failures : [];
      this.setBulkStatus(
        bulkStatusMessage(result, fallbackMessage),
        failures.length > 0 ? "is-error" : "is-success"
      );
      this.clearPreviewState();
      this.selectedKeys.clear();
      this.updateBulkControls();
      this.ignoreNextManagementMutationClear = true;
      window.dispatchEvent(new CustomEvent(managementMutationEventName, {
        detail: result,
      }));
      this.scheduleSearch(0);
    } catch {
      this.reportUnconfirmedBulkApply(unansweredRequest("write").message, operationID);
    } finally {
      this.setBulkBusy(false);
    }
  };

  protected runBulkAction = async (
    action: BulkAction,
    fields: BulkActionFields,
    fallbackMessage: string
  ): Promise<void> => {
    if (this.previewMatches(action, fields)) {
      await this.runBulkApply(action, fields, fallbackMessage);
      return;
    }
    await this.runBulkPreview(action, fields, fallbackMessage);
  };

  protected bindPreviewInvalidation = (form: HTMLFormElement | null): void => {
    if (!form) {
      return;
    }
    form.addEventListener("input", () => {
      this.clearPreviewState({ clearStatus: true });
    });
    form.addEventListener("change", () => {
      this.clearPreviewState({ clearStatus: true });
    });
  };

  protected bindBulkEvents = (): void => {
    this.els.trashButton?.addEventListener("click", () => {
      void this.runBulkAction("trash", {}, bulkApplyFallbackMessage("trash"));
    });

    this.els.moveForm?.addEventListener("submit", (event) => {
      event.preventDefault();
      void this.runBulkAction("move", readMoveFields(this.els.moveForm), bulkApplyFallbackMessage("move"));
    });

    this.els.setForm?.addEventListener("submit", (event) => {
      event.preventDefault();
      const fields = readSetFields(this.els.setForm);
      if (!fields.property) {
        this.setBulkStatus("Property is required.", "is-error");
        return;
      }
      void this.runBulkAction("frontmatter_set", fields, bulkApplyFallbackMessage("frontmatter_set"));
    });

    this.els.unsetForm?.addEventListener("submit", (event) => {
      event.preventDefault();
      const fields = readUnsetFields(this.els.unsetForm);
      if (!fields.property) {
        this.setBulkStatus("Property is required.", "is-error");
        return;
      }
      void this.runBulkAction("frontmatter_unset", fields, bulkApplyFallbackMessage("frontmatter_unset"));
    });

    this.els.previewApplyButton?.addEventListener("click", () => {
      if (!this.previewState) {
        this.setBulkStatus("Preview the bulk action before applying it.", "is-error");
        return;
      }
      void this.runBulkApply(
        this.previewState.action,
        this.readFieldsForAction(this.previewState.action),
        bulkApplyFallbackMessage(this.previewState.action)
      );
    });

    this.els.previewClearButton?.addEventListener("click", () => {
      this.clearPreviewState({ clearStatus: true });
    });

  };
}
