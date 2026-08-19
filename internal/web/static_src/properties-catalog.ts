import {
  normalizeOpenErrorMessage,
  normalizeManagementErrorMessage,
  normalizePropertiesResponse,
  type PropertyProposal,
  type PropertySchemaDefinitionInput,
} from "./api";
import { queryElement } from "./dom";
import {
  catalogStatusMessage,
  configStatusMessage,
  filterCatalog,
  readPropertiesConfig,
  type CatalogFilter,
  type PropertiesDeps,
} from "./properties-model";
import { renderCatalog, renderProposals, renderTotals } from "./properties-render";
import { createPreviewDialog, previewSummaryLabel } from "./properties-preview-dialog";
import { applyPropertySchema, previewPropertySchema } from "./properties-workbench-requests";
import { isPropertyKind, isPropertyScalarKind } from "./properties-value-control";
import { setStatusText, type StatusKind } from "./status";

const readPayload = async (resp: Response): Promise<unknown> => {
  try {
    return await resp.json();
  } catch {
    return null;
  }
};

export const buildOpenConfigFetchRequest = (token: string): RequestInit => ({
  method: "POST",
  headers: {
    Accept: "application/json",
    "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
    "X-BN-CSRF": token,
  },
  body: new URLSearchParams({ token }).toString(),
});

export const inferredDefinition = (
  proposal: PropertyProposal
): PropertySchemaDefinitionInput | null => {
  if (!isPropertyKind(proposal.type)) {
    return null;
  }
  if (proposal.type === "list") {
    if (!proposal.items || !isPropertyScalarKind(proposal.items)) {
      return null;
    }
    return {
      type: proposal.type,
      items: proposal.items,
      required: false,
      allowed: [],
    };
  }
  return {
    type: proposal.type,
    required: false,
    allowed: [],
  };
};

export const initPropertiesCatalog = (deps: PropertiesDeps = {}): void => {
  const root = queryElement(document, "[data-properties-catalog]", HTMLElement);
  if (!root) {
    return;
  }
  const statusEl = queryElement(root, "[data-properties-status]", HTMLElement);
  const listEl = queryElement(root, "[data-properties-list]", HTMLElement);
  if (!statusEl || !listEl) {
    return;
  }

  const configEl = queryElement(root, "[data-properties-config]", HTMLElement);
  const totalsEl = queryElement(root, "[data-properties-totals]", HTMLElement);
  const proposalsEl = queryElement(root, "[data-properties-proposals]", HTMLElement);
  const proposalListEl = queryElement(root, "[data-properties-proposal-list]", HTMLElement);
  const configOpenEl = queryElement(root, "[data-properties-config-open]", HTMLButtonElement);
  const dialogHost = queryElement(root, "[data-properties-dialog-host]", HTMLElement);
  const queryEl = queryElement(root, "[data-properties-query]", HTMLInputElement);
  const issuesOnlyEl = queryElement(root, "[data-properties-issues-only]", HTMLInputElement);
  const refreshEl = queryElement(root, "[data-properties-refresh]", HTMLButtonElement);

  const config = readPropertiesConfig(root);
  const fetcher = deps.fetch || fetch;
  const dialog = config.token !== "" && dialogHost
    ? createPreviewDialog(root.ownerDocument, dialogHost)
    : null;
  let latest: ReturnType<typeof normalizePropertiesResponse> | null = null;
  let mutationBusy = false;

  const setStatus = (message: string, kind: StatusKind = ""): void => {
    setStatusText(statusEl, message, kind);
  };

  const currentFilter = (): CatalogFilter => ({
    query: queryEl?.value || "",
    issuesOnly: issuesOnlyEl?.checked || false,
  });

  const setMutationBusy = (busy: boolean): void => {
    mutationBusy = busy;
    for (const button of root.querySelectorAll<HTMLButtonElement>(
      "[data-properties-proposal-add]"
    )) {
      button.disabled = busy;
    }
  };

  const applyFilter = (): void => {
    if (!latest) {
      return;
    }
    const shown = filterCatalog(latest.properties, currentFilter());
    renderCatalog(listEl, shown);
    setStatus(
      catalogStatusMessage(latest, shown.length),
      shown.length === 0 ? "is-empty" : ""
    );
  };

  const applyProposal = async (proposal: PropertyProposal, previewID: string): Promise<void> => {
    setMutationBusy(true);
    setStatus("Adding the inferred definition...", "is-loading");
    try {
      const outcome = await applyPropertySchema(fetcher, config.token, previewID);
      if (!outcome.ok) {
        setStatus(outcome.message, "is-error");
        return;
      }
      await load();
      setStatus(`Added ${proposal.property} to brainnotes.toml.`, "is-success");
    } finally {
      setMutationBusy(false);
    }
  };

  const addProposal = async (proposal: PropertyProposal): Promise<void> => {
    if (mutationBusy) {
      return;
    }
    const definition = inferredDefinition(proposal);
    if (!dialog || config.token === "" || !definition) {
      setStatus("This inferred definition cannot be added.", "is-error");
      return;
    }
    setMutationBusy(true);
    setStatus("Previewing the inferred definition...", "is-loading");
    try {
      const outcome = await previewPropertySchema(fetcher, config.token, {
        op: "upsert",
        property: proposal.property,
        definition,
      });
      if (!outcome.ok) {
        setStatus(outcome.message, "is-error");
        return;
      }
      const result = outcome.result;
      setStatus(previewSummaryLabel(result));
      dialog.open({
        title: `Add ${proposal.property} to brainnotes.toml`,
        summary: previewSummaryLabel(result),
        confirmLabel: "Add definition",
        result,
        onConfirm: () => {
          void applyProposal(proposal, result.preview_id);
        },
        onCancel: () => {
          setStatus("Definition change canceled.");
        },
      });
    } finally {
      setMutationBusy(false);
    }
  };

  const load = async (): Promise<void> => {
    root.setAttribute("aria-busy", "true");
    setStatus("Loading properties...", "is-loading");
    try {
      const resp = await fetcher(config.endpoint, { headers: { Accept: "application/json" } });
      const payload = await readPayload(resp);
      if (!resp.ok) {
        setStatus(normalizeManagementErrorMessage(resp.status, payload), "is-error");
        return;
      }
      const result = normalizePropertiesResponse(payload);
      if (!result.ok) {
        setStatus(result.message || "Properties unavailable.", "is-error");
        return;
      }
      latest = result;
      renderTotals(totalsEl, result.totals, result.total_notes);
      renderProposals(proposalsEl, proposalListEl, result.proposals, (proposal) => {
        void addProposal(proposal);
      });
      if (configEl) {
        const message = configStatusMessage(result.config);
        configEl.textContent = message;
        configEl.hidden = message === "";
      }
      applyFilter();
    } catch {
      setStatus("Properties unavailable.", "is-error");
    } finally {
      root.setAttribute("aria-busy", "false");
    }
  };

  queryEl?.addEventListener("input", applyFilter);
  issuesOnlyEl?.addEventListener("change", applyFilter);
  refreshEl?.addEventListener("click", () => {
    void load();
  });
  configOpenEl?.addEventListener("click", async () => {
    if (config.token === "") {
      setStatus("Open action unavailable.", "is-error");
      return;
    }
    configOpenEl.disabled = true;
    setStatus("Opening brainnotes.toml in the configured editor...", "is-loading");
    try {
      const resp = await fetcher("/api/config/open", buildOpenConfigFetchRequest(config.token));
      if (!resp.ok) {
        setStatus(normalizeOpenErrorMessage(resp.status, await resp.text()), "is-error");
        return;
      }
      setStatus("Opened brainnotes.toml in the configured editor.", "is-success");
    } catch {
      setStatus("Open failed.", "is-error");
    } finally {
      configOpenEl.disabled = false;
    }
  });

  void load();
};
