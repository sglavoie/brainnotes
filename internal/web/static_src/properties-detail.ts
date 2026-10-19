import {
  buildPropertyDetailURL,
  normalizeManagementErrorMessage,
  normalizePropertyDetailResponse,
  normalizePropertyRowFilterStatus,
  type PropertyConfigStatus,
  type PropertyDetailResult,
} from "./api";
import { queryElement } from "./dom";
import {
  PROPERTY_ROW_LIMIT,
  configStatusMessage,
  detailStatusMessage,
  emptyDetailFilter,
  nextOffset,
  previousOffset,
  readPropertyDetailConfig,
  type DetailFilter,
  type PropertiesDeps,
} from "./properties-model";
import {
  renderDefinition,
  renderPagination,
  renderProposal,
  renderRows,
  renderSummary,
  renderValues,
} from "./properties-render";
import {
  configInvalidMessage,
  configRestartMessage,
  createConfigStatusPoller,
} from "./properties-config-status";
import { createPropertySchemaEditor } from "./properties-schema-editor";
import { createPropertyWorkbench, type PropertyWorkbench } from "./properties-workbench";
import { setStatusText, type StatusKind } from "./status";

const readPayload = async (resp: Response): Promise<unknown> => {
  try {
    return await resp.json();
  } catch {
    return null;
  }
};

const queryDetailElements = (root: HTMLElement) => ({
  root,
  statusEl: queryElement(root, "[data-property-status]", HTMLElement),
  rowsEl: queryElement(root, "[data-property-rows]", HTMLElement),
  configEl: queryElement(root, "[data-property-config]", HTMLElement),
  definitionEl: queryElement(root, "[data-property-definition]", HTMLElement),
  summaryEl: queryElement(root, "[data-property-summary]", HTMLElement),
  proposalEl: queryElement(root, "[data-property-proposal]", HTMLElement),
  valuesEl: queryElement(root, "[data-property-values]", HTMLElement),
  statusFilterEl: queryElement(root, "[data-property-filter-status]", HTMLSelectElement),
  queryEl: queryElement(root, "[data-property-filter-query]", HTMLInputElement),
  clearValueEl: queryElement(root, "[data-property-filter-clear]", HTMLButtonElement),
  activeValueEl: queryElement(root, "[data-property-active-value]", HTMLElement),
  paginationEl: queryElement(root, "[data-property-pagination]", HTMLElement),
  prevEl: queryElement(root, "[data-property-prev]", HTMLButtonElement),
  nextEl: queryElement(root, "[data-property-next]", HTMLButtonElement),
  refreshEl: queryElement(root, "[data-property-refresh]", HTMLButtonElement),
  invalidEl: queryElement(root, "[data-property-config-invalid]", HTMLElement),
  restartEl: queryElement(root, "[data-property-config-restart]", HTMLElement),
});

type DetailElements = ReturnType<typeof queryDetailElements> & {
  statusEl: HTMLElement;
  rowsEl: HTMLElement;
};

type DetailView = {
  els: DetailElements;
  filter: DetailFilter;
  workbench: PropertyWorkbench | null;
  schemaEditor: { sync: (property: PropertyDetailResult["property"]) => void } | null;
  setStatus: (message: string, kind?: StatusKind) => void;
};

const showMessage = (element: HTMLElement | null, message: string): void => {
  if (!element) return;
  element.textContent = message;
  element.hidden = message === "";
};

const renderBanners = (els: DetailElements, status: PropertyConfigStatus): void => {
  showMessage(els.invalidEl, configInvalidMessage(status));
  showMessage(els.restartEl, configRestartMessage(status));
};

const renderActiveValue = (els: DetailElements, filter: DetailFilter): void => {
  showMessage(els.activeValueEl, filter.value === "" ? "" : `Filtered to the value "${filter.value}".`);
  if (els.clearValueEl) els.clearValueEl.hidden = filter.value === "";
};

const renderPager = (els: DetailElements, pagination: PropertyDetailResult["pagination"]): void => {
  renderPagination(els.paginationEl, pagination);
  if (els.prevEl) {
    els.prevEl.disabled = pagination.offset === 0;
    els.prevEl.setAttribute(
      "data-property-prev-offset",
      String(previousOffset(pagination, pagination.limit))
    );
  }
  if (els.nextEl) {
    els.nextEl.disabled = !pagination.has_more;
    if (pagination.has_more) {
      els.nextEl.setAttribute("data-property-next-offset", String(nextOffset(pagination)));
    }
  }
};

const renderDetail = (view: DetailView, result: PropertyDetailResult): void => {
  const { els, filter } = view;
  renderSummary(els.summaryEl, result.property);
  renderDefinition(els.definitionEl, result.property);
  renderProposal(els.proposalEl, result.property);
  renderValues(els.valuesEl, result.values, filter.value);
  if (view.workbench) view.workbench.render(result);
  else renderRows(els.rowsEl, result.rows);
  view.schemaEditor?.sync(result.property);
  renderActiveValue(els, filter);
  renderBanners(els, result.config);
  showMessage(els.configEl, configStatusMessage(result.config));
  renderPager(els, result.pagination);
  // The server clamps and normalizes the request, so the echoed pagination
  // is the truth about which page is on screen.
  filter.offset = result.pagination.offset;
  view.setStatus(detailStatusMessage(result), result.pagination.total === 0 ? "is-empty" : "");
};

type DetailRead =
  | { ok: true; result: PropertyDetailResult }
  | { ok: false; message: string };

const readDetail = async (
  fetcher: NonNullable<PropertiesDeps["fetch"]>,
  url: string
): Promise<DetailRead> => {
  try {
    const resp = await fetcher(url, { headers: { Accept: "application/json" } });
    const payload = await readPayload(resp);
    if (!resp.ok) return { ok: false, message: normalizeManagementErrorMessage(resp.status, payload) };
    const result = normalizePropertyDetailResponse(payload);
    if (!result.ok) return { ok: false, message: result.message || "Property unavailable." };
    return { ok: true, result };
  } catch {
    return { ok: false, message: "Property unavailable." };
  }
};

// Filters, paging, writes and configuration changes can each start a read.
// Only the latest one may render or clear the busy state, so a slow earlier
// answer never replaces the page the person asked for last; it resolves true
// only when its own answer rendered.
const createDetailLoader = (
  view: DetailView,
  fetcher: NonNullable<PropertiesDeps["fetch"]>,
  endpoint: string,
  property: string
): (() => Promise<boolean>) => {
  let issued = 0;
  return async () => {
    const issue = ++issued;
    view.els.root.setAttribute("aria-busy", "true");
    view.setStatus("Loading property…", "is-loading");
    const { filter } = view;
    const url = buildPropertyDetailURL(endpoint, {
      property,
      status: filter.status,
      value: filter.value,
      q: filter.query,
      limit: PROPERTY_ROW_LIMIT,
      offset: filter.offset,
    });
    const read = await readDetail(fetcher, url);
    if (issue !== issued) return false;
    view.els.root.setAttribute("aria-busy", "false");
    if (!read.ok) {
      view.setStatus(read.message, "is-error");
      return false;
    }
    renderDetail(view, read.result);
    return true;
  };
};

const bindFilterControls = (view: DetailView, load: () => Promise<boolean>): void => {
  const { els, filter } = view;
  const reloadFrom = (offset: number): void => {
    filter.offset = offset;
    void load();
  };
  els.statusFilterEl?.addEventListener("change", () => {
    filter.status = normalizePropertyRowFilterStatus(els.statusFilterEl?.value || "");
    reloadFrom(0);
  });
  els.queryEl?.addEventListener("change", () => {
    filter.query = els.queryEl?.value || "";
    reloadFrom(0);
  });
  els.clearValueEl?.addEventListener("click", () => {
    filter.value = "";
    reloadFrom(0);
  });
  els.refreshEl?.addEventListener("click", () => {
    void load();
  });
  els.prevEl?.addEventListener("click", () => {
    reloadFrom(Number(els.prevEl?.getAttribute("data-property-prev-offset") || "0"));
  });
  els.nextEl?.addEventListener("click", () => {
    reloadFrom(Number(els.nextEl?.getAttribute("data-property-next-offset") || "0"));
  });
};

const bindValueFilter = (view: DetailView, load: () => Promise<boolean>): void => {
  const { valuesEl } = view.els;
  valuesEl?.addEventListener("click", (event) => {
    const target = event.target;
    const button = target instanceof Element
      ? target.closest<HTMLButtonElement>("[data-property-value]")
      : null;
    if (!button || !valuesEl.contains(button) || button.disabled) {
      return;
    }
    const value = button.getAttribute("data-property-value") || "";
    view.filter.value = view.filter.value === value ? "" : value;
    view.filter.offset = 0;
    void load();
  });
};

const createDetailView = (
  els: DetailElements,
  config: ReturnType<typeof readPropertyDetailConfig>,
  deps: PropertiesDeps,
  reload: () => Promise<boolean>
): DetailView => {
  const setStatus = (message: string, kind: StatusKind = ""): void => {
    setStatusText(els.statusEl, message, kind);
  };
  const workbench = config.token === ""
    ? null
    : createPropertyWorkbench(
        {
          root: els.root,
          rowsEl: els.rowsEl,
          property: config.property,
          token: config.token,
          historyHref: config.historyPath,
          reload,
          setStatus,
        },
        deps
      );
  const schemaEditor = workbench
    ? createPropertySchemaEditor({
        root: els.root,
        submit: (op, definition) => {
          void workbench.previewSchema(op, definition);
        },
        setStatus: (message) => {
          setStatus(message);
        },
      })
    : null;
  return { els, filter: emptyDetailFilter(), workbench, schemaEditor, setStatus };
};

export const initPropertyDetail = (deps: PropertiesDeps = {}): void => {
  const root = queryElement(document, "[data-property-detail]", HTMLElement);
  if (!root) {
    return;
  }
  const found = queryDetailElements(root);
  const { statusEl, rowsEl } = found;
  const config = readPropertyDetailConfig(root);
  if (!statusEl || !rowsEl || config.property === "") {
    return;
  }
  const els: DetailElements = { ...found, statusEl, rowsEl };
  const fetcher = deps.fetch || fetch;
  const loader: { load: () => Promise<boolean> } = { load: async () => false };
  const view = createDetailView(els, config, deps, () => loader.load());
  loader.load = createDetailLoader(view, fetcher, config.detailEndpoint, config.property);
  bindFilterControls(view, loader.load);
  bindValueFilter(view, loader.load);

  void loader.load();

  // Only a writable page watches the configuration: the banners exist to say
  // whether a write can succeed, so a read-only render has nothing to report.
  if (config.token !== "") {
    createConfigStatusPoller(document, {
      fetch: fetcher,
      onStatus: (status) => renderBanners(els, status),
      onGenerationChange: () => {
        view.setStatus("brainnotes.toml changed on disk. Reloading…", "is-loading");
        void loader.load();
      },
    }).start();
  }
};
