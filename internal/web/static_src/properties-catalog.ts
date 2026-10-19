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
  type FetchLike,
  type PropertiesConfig,
  type PropertiesDeps,
} from "./properties-model";
import { renderCatalog, renderProposals, renderTotals } from "./properties-render";
import { createPreviewDialog, previewSummaryLabel, type PreviewDialog } from "./properties-preview-dialog";
import { applyPropertySchema, previewPropertySchema } from "./properties-workbench-requests";
import { isPropertyKind, isPropertyScalarKind } from "./properties-value-control";
import { setStatusText, type StatusKind } from "./status";
import { newWriteOperationID, offerWriteOutcomeCheck } from "./write-outcome-check";

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

const queryCatalogElements = (root: HTMLElement) => ({
  root,
  statusEl: queryElement(root, "[data-properties-status]", HTMLElement),
  listEl: queryElement(root, "[data-properties-list]", HTMLElement),
  configEl: queryElement(root, "[data-properties-config]", HTMLElement),
  totalsEl: queryElement(root, "[data-properties-totals]", HTMLElement),
  proposalsEl: queryElement(root, "[data-properties-proposals]", HTMLElement),
  proposalListEl: queryElement(root, "[data-properties-proposal-list]", HTMLElement),
  configOpenEl: queryElement(root, "[data-properties-config-open]", HTMLButtonElement),
  dialogHost: queryElement(root, "[data-properties-dialog-host]", HTMLElement),
  queryEl: queryElement(root, "[data-properties-query]", HTMLInputElement),
  issuesOnlyEl: queryElement(root, "[data-properties-issues-only]", HTMLInputElement),
  refreshEl: queryElement(root, "[data-properties-refresh]", HTMLButtonElement),
});

type CatalogElements = ReturnType<typeof queryCatalogElements> & {
  statusEl: HTMLElement;
  listEl: HTMLElement;
};

type Catalog = {
  els: CatalogElements;
  config: PropertiesConfig;
  fetcher: FetchLike;
  dialog: PreviewDialog | null;
  latest: ReturnType<typeof normalizePropertiesResponse> | null;
  mutationBusy: boolean;
  setStatus: (message: string, kind?: StatusKind) => void;
};

const currentFilter = (els: CatalogElements): CatalogFilter => ({
  query: els.queryEl?.value || "",
  issuesOnly: els.issuesOnlyEl?.checked || false,
});

const setMutationBusy = (catalog: Catalog, busy: boolean): void => {
  catalog.mutationBusy = busy;
  for (const button of catalog.els.root.querySelectorAll<HTMLButtonElement>(
    "[data-properties-proposal-add]"
  )) {
    button.disabled = busy;
  }
};

const applyFilter = (catalog: Catalog): void => {
  if (!catalog.latest) {
    return;
  }
  const shown = filterCatalog(catalog.latest.properties, currentFilter(catalog.els));
  renderCatalog(catalog.els.listEl, shown);
  catalog.setStatus(
    catalogStatusMessage(catalog.latest, shown.length),
    shown.length === 0 ? "is-empty" : ""
  );
};

const renderLoaded = (
  catalog: Catalog,
  result: ReturnType<typeof normalizePropertiesResponse>
): void => {
  const { els } = catalog;
  catalog.latest = result;
  renderTotals(els.totalsEl, result.totals, result.total_notes);
  renderProposals(els.proposalsEl, els.proposalListEl, result.proposals, (proposal) => {
    void addProposal(catalog, proposal);
  });
  if (els.configEl) {
    const message = configStatusMessage(result.config);
    els.configEl.textContent = message;
    els.configEl.hidden = message === "";
  }
  applyFilter(catalog);
};

// Resolves true only when the catalog rendered, so a caller reporting a write
// does not cover a failed re-read with its confirmation.
const loadCatalog = async (catalog: Catalog): Promise<boolean> => {
  const { root } = catalog.els;
  root.setAttribute("aria-busy", "true");
  catalog.setStatus("Loading properties…", "is-loading");
  try {
    const resp = await catalog.fetcher(catalog.config.endpoint, { headers: { Accept: "application/json" } });
    const payload = await readPayload(resp);
    if (!resp.ok) {
      catalog.setStatus(normalizeManagementErrorMessage(resp.status, payload), "is-error");
      return false;
    }
    const result = normalizePropertiesResponse(payload);
    if (!result.ok) {
      catalog.setStatus(result.message || "Properties unavailable.", "is-error");
      return false;
    }
    renderLoaded(catalog, result);
    return true;
  } catch {
    catalog.setStatus("Properties unavailable.", "is-error");
    return false;
  } finally {
    root.setAttribute("aria-busy", "false");
  }
};

// A lost reply may still have written brainnotes.toml, so it is checked by its
// attempt ID rather than retried; a confirmed outcome re-reads the catalog and
// keeps the confirmation and its History link in view.
const offerProposalOutcomeCheck = (catalog: Catalog, operationID: string): void => {
  const status = catalog.els.statusEl;
  offerWriteOutcomeCheck(status, operationID, catalog.fetcher, "Definition change", () => {
    const confirmation = [...status.childNodes];
    void loadCatalog(catalog).then((loaded) => {
      if (!loaded) {
        status.append(" ", ...confirmation);
        return;
      }
      catalog.setStatus("", "is-success");
      status.replaceChildren(...confirmation);
    });
  });
};

const applyProposal = async (
  catalog: Catalog,
  proposal: PropertyProposal,
  previewID: string
): Promise<void> => {
  setMutationBusy(catalog, true);
  const operationID = newWriteOperationID();
  catalog.setStatus("Adding the inferred definition…", "is-loading");
  try {
    const outcome = await applyPropertySchema(catalog.fetcher, catalog.config.token, previewID, operationID);
    if (!outcome.ok) {
      catalog.setStatus(outcome.message, "is-error");
      if (outcome.uncertain) offerProposalOutcomeCheck(catalog, operationID);
      return;
    }
    if (await loadCatalog(catalog)) {
      catalog.setStatus(`Added ${proposal.property} to brainnotes.toml.`, "is-success");
    }
  } finally {
    setMutationBusy(catalog, false);
  }
};

const addProposal = async (catalog: Catalog, proposal: PropertyProposal): Promise<void> => {
  if (catalog.mutationBusy) {
    return;
  }
  const definition = inferredDefinition(proposal);
  const { dialog } = catalog;
  if (!dialog || catalog.config.token === "" || !definition) {
    catalog.setStatus("This inferred definition cannot be added.", "is-error");
    return;
  }
  setMutationBusy(catalog, true);
  catalog.setStatus("Previewing the inferred definition…", "is-loading");
  try {
    const outcome = await previewPropertySchema(catalog.fetcher, catalog.config.token, {
      op: "upsert",
      property: proposal.property,
      definition,
    });
    if (!outcome.ok) {
      catalog.setStatus(outcome.message, "is-error");
      return;
    }
    const result = outcome.result;
    catalog.setStatus(previewSummaryLabel(result));
    dialog.open({
      title: `Add ${proposal.property} to brainnotes.toml`,
      summary: previewSummaryLabel(result),
      confirmLabel: "Add definition",
      result,
      onConfirm: () => {
        void applyProposal(catalog, proposal, result.preview_id);
      },
      onCancel: () => {
        catalog.setStatus("Definition change canceled.");
      },
    });
  } finally {
    setMutationBusy(catalog, false);
  }
};

const openConfig = async (catalog: Catalog, button: HTMLButtonElement): Promise<void> => {
  if (catalog.config.token === "") {
    catalog.setStatus("Open action unavailable.", "is-error");
    return;
  }
  button.disabled = true;
  catalog.setStatus("Opening brainnotes.toml in the configured editor…", "is-loading");
  try {
    const resp = await catalog.fetcher("/api/config/open", buildOpenConfigFetchRequest(catalog.config.token));
    if (!resp.ok) {
      catalog.setStatus(normalizeOpenErrorMessage(resp.status, await resp.text()), "is-error");
      return;
    }
    catalog.setStatus("Opened brainnotes.toml in the configured editor.", "is-success");
  } catch {
    catalog.setStatus("Open failed.", "is-error");
  } finally {
    button.disabled = false;
  }
};

const bindCatalog = (catalog: Catalog): void => {
  const { els } = catalog;
  const filter = (): void => applyFilter(catalog);
  els.queryEl?.addEventListener("input", filter);
  els.issuesOnlyEl?.addEventListener("change", filter);
  els.refreshEl?.addEventListener("click", () => {
    void loadCatalog(catalog);
  });
  const { configOpenEl } = els;
  configOpenEl?.addEventListener("click", () => {
    void openConfig(catalog, configOpenEl);
  });
};

export const initPropertiesCatalog = (deps: PropertiesDeps = {}): void => {
  const root = queryElement(document, "[data-properties-catalog]", HTMLElement);
  if (!root) {
    return;
  }
  const found = queryCatalogElements(root);
  const { statusEl, listEl } = found;
  if (!statusEl || !listEl) {
    return;
  }
  const config = readPropertiesConfig(root);
  const catalog: Catalog = {
    els: { ...found, statusEl, listEl },
    config,
    fetcher: deps.fetch || fetch,
    dialog: config.token !== "" && found.dialogHost
      ? createPreviewDialog(root.ownerDocument, found.dialogHost)
      : null,
    latest: null,
    mutationBusy: false,
    setStatus: (message, kind = "") => {
      setStatusText(statusEl, message, kind);
    },
  };
  bindCatalog(catalog);
  void loadCatalog(catalog);
};
