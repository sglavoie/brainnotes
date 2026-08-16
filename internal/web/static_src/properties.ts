import {
  buildPropertyDetailURL,
  normalizeManagementErrorMessage,
  normalizePropertiesResponse,
  normalizePropertyDetailResponse,
  normalizePropertyRowFilterStatus,
  type PropertyConfigStatus,
} from "./api";
import { queryElement } from "./dom";
import {
  PROPERTY_ROW_LIMIT,
  catalogStatusMessage,
  configStatusMessage,
  detailStatusMessage,
  emptyDetailFilter,
  filterCatalog,
  nextOffset,
  previousOffset,
  readPropertiesConfig,
  readPropertyDetailConfig,
  type CatalogFilter,
  type DetailFilter,
  type PropertiesDeps,
} from "./properties-model";
import {
  renderCatalog,
  renderDefinition,
  renderPagination,
  renderProposal,
  renderProposals,
  renderRows,
  renderSummary,
  renderTotals,
  renderValues,
} from "./properties-render";
import {
  configInvalidMessage,
  configRestartMessage,
  createConfigStatusPoller,
} from "./properties-config-status";
import { createPropertySchemaEditor } from "./properties-schema-editor";
import { createPropertyWorkbench } from "./properties-workbench";
import { setStatusText, type StatusKind } from "./status";

export {
  catalogStatusMessage,
  configStatusMessage,
  coverageLabel,
  definitionLabel,
  detailStatusMessage,
  filterCatalog,
  issueLabel,
  issueSummaryLabel,
  nextOffset,
  paginationLabel,
  previousOffset,
  propertyKindLabel,
  readPropertiesConfig,
  readPropertyDetailConfig,
  valueDisplayText,
  type CatalogFilter,
  type DetailFilter,
  type PropertiesConfig,
  type PropertiesDeps,
  type PropertyDetailConfig,
} from "./properties-model";
export {
  renderCatalog,
  renderProposals,
  renderRows,
  renderValues,
} from "./properties-render";

const readPayload = async (resp: Response): Promise<unknown> => {
  try {
    return await resp.json();
  } catch {
    return null;
  }
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
  const queryEl = queryElement(root, "[data-properties-query]", HTMLInputElement);
  const issuesOnlyEl = queryElement(root, "[data-properties-issues-only]", HTMLInputElement);
  const refreshEl = queryElement(root, "[data-properties-refresh]", HTMLButtonElement);

  const config = readPropertiesConfig(root);
  const fetcher = deps.fetch || fetch;
  let latest: ReturnType<typeof normalizePropertiesResponse> | null = null;

  const setStatus = (message: string, kind: StatusKind = ""): void => {
    setStatusText(statusEl, message, kind);
  };

  const currentFilter = (): CatalogFilter => ({
    query: queryEl?.value || "",
    issuesOnly: issuesOnlyEl?.checked || false,
  });

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
      renderProposals(proposalsEl, proposalListEl, result.proposals);
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

  void load();
};

export const initPropertyDetail = (deps: PropertiesDeps = {}): void => {
  const root = queryElement(document, "[data-property-detail]", HTMLElement);
  if (!root) {
    return;
  }
  const statusEl = queryElement(root, "[data-property-status]", HTMLElement);
  const rowsEl = queryElement(root, "[data-property-rows]", HTMLElement);
  if (!statusEl || !rowsEl) {
    return;
  }

  const configEl = queryElement(root, "[data-property-config]", HTMLElement);
  const definitionEl = queryElement(root, "[data-property-definition]", HTMLElement);
  const summaryEl = queryElement(root, "[data-property-summary]", HTMLElement);
  const proposalEl = queryElement(root, "[data-property-proposal]", HTMLElement);
  const valuesEl = queryElement(root, "[data-property-values]", HTMLElement);
  const statusFilterEl = queryElement(root, "[data-property-filter-status]", HTMLSelectElement);
  const queryEl = queryElement(root, "[data-property-filter-query]", HTMLInputElement);
  const clearValueEl = queryElement(root, "[data-property-filter-clear]", HTMLButtonElement);
  const activeValueEl = queryElement(root, "[data-property-active-value]", HTMLElement);
  const paginationEl = queryElement(root, "[data-property-pagination]", HTMLElement);
  const prevEl = queryElement(root, "[data-property-prev]", HTMLButtonElement);
  const nextEl = queryElement(root, "[data-property-next]", HTMLButtonElement);
  const refreshEl = queryElement(root, "[data-property-refresh]", HTMLButtonElement);

  const config = readPropertyDetailConfig(root);
  if (config.property === "") {
    return;
  }
  const fetcher = deps.fetch || fetch;
  const filter: DetailFilter = emptyDetailFilter();
  const invalidEl = queryElement(root, "[data-property-config-invalid]", HTMLElement);
  const restartEl = queryElement(root, "[data-property-config-restart]", HTMLElement);

  const setStatus = (message: string, kind: StatusKind = ""): void => {
    setStatusText(statusEl, message, kind);
  };

  const renderBanners = (status: PropertyConfigStatus): void => {
    if (invalidEl) {
      const message = configInvalidMessage(status);
      invalidEl.textContent = message;
      invalidEl.hidden = message === "";
    }
    if (restartEl) {
      const message = configRestartMessage(status);
      restartEl.textContent = message;
      restartEl.hidden = message === "";
    }
  };

  const workbench = config.token === ""
    ? null
    : createPropertyWorkbench(
        {
          root,
          rowsEl,
          property: config.property,
          token: config.token,
          historyHref: config.historyPath,
          reload: () => load(),
          setStatus,
        },
        deps
      );
  const schemaEditor = workbench
    ? createPropertySchemaEditor({
        root,
        submit: (op, definition) => {
          void workbench.previewSchema(op, definition);
        },
        setStatus: (message) => {
          setStatus(message);
        },
      })
    : null;

  const renderActiveValue = (): void => {
    if (activeValueEl) {
      activeValueEl.textContent =
        filter.value === "" ? "" : `Filtered to the value "${filter.value}".`;
      activeValueEl.hidden = filter.value === "";
    }
    if (clearValueEl) {
      clearValueEl.hidden = filter.value === "";
    }
  };

  const load = async (): Promise<void> => {
    root.setAttribute("aria-busy", "true");
    setStatus("Loading property...", "is-loading");
    try {
      const url = buildPropertyDetailURL(config.detailEndpoint, {
        property: config.property,
        status: filter.status,
        value: filter.value,
        q: filter.query,
        limit: PROPERTY_ROW_LIMIT,
        offset: filter.offset,
      });
      const resp = await fetcher(url, { headers: { Accept: "application/json" } });
      const payload = await readPayload(resp);
      if (!resp.ok) {
        setStatus(normalizeManagementErrorMessage(resp.status, payload), "is-error");
        return;
      }
      const result = normalizePropertyDetailResponse(payload);
      if (!result.ok) {
        setStatus(result.message || "Property unavailable.", "is-error");
        return;
      }

      renderSummary(summaryEl, result.property);
      renderDefinition(definitionEl, result.property);
      renderProposal(proposalEl, result.property);
      renderValues(valuesEl, result.values, filter.value);
      if (workbench) {
        workbench.render(result);
      } else {
        renderRows(rowsEl, result.rows);
      }
      schemaEditor?.sync(result.property);
      renderPagination(paginationEl, result.pagination);
      renderActiveValue();
      renderBanners(result.config);
      if (configEl) {
        const message = configStatusMessage(result.config);
        configEl.textContent = message;
        configEl.hidden = message === "";
      }
      if (prevEl) {
        prevEl.disabled = result.pagination.offset === 0;
      }
      if (nextEl) {
        nextEl.disabled = !result.pagination.has_more;
      }
      // The server clamps and normalizes the request, so the echoed pagination
      // is the truth about which page is on screen.
      filter.offset = result.pagination.offset;
      setStatus(
        detailStatusMessage(result),
        result.pagination.total === 0 ? "is-empty" : ""
      );
      if (nextEl && result.pagination.has_more) {
        nextEl.setAttribute("data-property-next-offset", String(nextOffset(result.pagination)));
      }
      if (prevEl) {
        prevEl.setAttribute(
          "data-property-prev-offset",
          String(previousOffset(result.pagination, result.pagination.limit))
        );
      }
    } catch {
      setStatus("Property unavailable.", "is-error");
    } finally {
      root.setAttribute("aria-busy", "false");
    }
  };

  statusFilterEl?.addEventListener("change", () => {
    filter.status = normalizePropertyRowFilterStatus(statusFilterEl.value);
    filter.offset = 0;
    void load();
  });
  queryEl?.addEventListener("change", () => {
    filter.query = queryEl.value;
    filter.offset = 0;
    void load();
  });
  clearValueEl?.addEventListener("click", () => {
    filter.value = "";
    filter.offset = 0;
    void load();
  });
  refreshEl?.addEventListener("click", () => {
    void load();
  });
  prevEl?.addEventListener("click", () => {
    filter.offset = Number(prevEl.getAttribute("data-property-prev-offset") || "0");
    void load();
  });
  nextEl?.addEventListener("click", () => {
    filter.offset = Number(nextEl.getAttribute("data-property-next-offset") || "0");
    void load();
  });
  valuesEl?.addEventListener("click", (event) => {
    const target = event.target;
    if (!(target instanceof Element)) {
      return;
    }
    const button = target.closest<HTMLButtonElement>("[data-property-value]");
    if (!button || !valuesEl.contains(button) || button.disabled) {
      return;
    }
    const value = button.getAttribute("data-property-value") || "";
    filter.value = filter.value === value ? "" : value;
    filter.offset = 0;
    void load();
  });

  void load();

  // Only a writable page watches the configuration: the banners exist to say
  // whether a write can succeed, so a read-only render has nothing to report.
  if (config.token !== "") {
    createConfigStatusPoller(document, {
      fetch: fetcher,
      onStatus: renderBanners,
      onGenerationChange: () => {
        setStatus("brainnotes.toml changed on disk. Reloading...", "is-loading");
        void load();
      },
    }).start();
  }
};

export const initProperties = (deps: PropertiesDeps = {}): void => {
  initPropertiesCatalog(deps);
  initPropertyDetail(deps);
};
