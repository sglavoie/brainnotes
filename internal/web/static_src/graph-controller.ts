import { getElementByID, queryAllElements, queryElement } from "./dom";
import {
  filterChangeEventName,
  readPageQuery,
  shouldHandleFilterChangeForMode,
  syncPageQueryParam,
} from "./filters";
import { type GraphDetailsRefs } from "./graph-controller-details";
import {
  rebuildDisplayForControl,
  scheduleDisplayRebuild,
  toggleFolderExpansion,
} from "./graph-controller-display";
import { handleGraphKeyDown, selectNode } from "./graph-controller-interaction";
import { bindGraphNoteList } from "./graph-note-list";
import { bindSearchInput } from "./search-input";
import { normalizeSearchMode } from "./graph-controller-labels";
import { updateSpreadLabel } from "./graph-controller-layout";
import { publishFilterChange, scheduleLoad, setSearchMode } from "./graph-controller-load";
import {
  canonicalizeLegacyDisplayParams,
  clampCollapseDepth,
  clampMinDegree,
  graphFilterDebounceMs,
  graphFilterMode,
  syncPageDisplayParams,
} from "./graph-controller-params";
import { type GraphRuntime, createGraphRuntime } from "./graph-controller-runtime";
import {
  fitCurrentGraph,
  restyleGraphForTheme,
  runLayout,
  setLabelMode,
  updateDisplayControls,
  updateZoomMatchesButton,
  zoomToMatches,
} from "./graph-controller-visual";
import { THEME_EVENT } from "./theme";

const bindControls = (runtime: GraphRuntime): void => {
  const { canvas, dom } = runtime;
  bindGraphNoteList(runtime, (id) => selectNode(runtime, id));
  canvas.addEventListener("keydown", (event) => {
    handleGraphKeyDown(runtime, event);
  });

  const openNote = dom.openNote;
  if (openNote instanceof HTMLAnchorElement) {
    openNote.addEventListener("click", (event) => {
      if (openNote.getAttribute("aria-disabled") === "true") {
        event.preventDefault();
      }
    });
  }

  const filterInput = dom.filterInput;
  if (filterInput instanceof HTMLInputElement) {
    bindSearchInput(filterInput, () => {
      syncPageQueryParam(filterInput.value.trim());
      publishFilterChange(runtime, graphFilterDebounceMs);
    }, () => {
      window.clearTimeout(runtime.publishToken);
      window.clearTimeout(runtime.debounceToken);
      runtime.requestToken += 1;
    });
    filterInput.addEventListener("keydown", (event) => {
      if (event.key !== "Enter" || event.isComposing) {
        return;
      }
      event.preventDefault();
      zoomToMatches(runtime);
    });
  }

  bindDisplayControls(runtime);

  if (dom.fitButton instanceof HTMLButtonElement) {
    dom.fitButton.addEventListener("click", () => {
      fitCurrentGraph(runtime);
    });
  }

  if (dom.zoomMatchesButton instanceof HTMLButtonElement) {
    dom.zoomMatchesButton.addEventListener("click", () => {
      zoomToMatches(runtime);
    });
  }
  updateZoomMatchesButton(runtime);

  if (dom.resetLayoutButton instanceof HTMLButtonElement) {
    dom.resetLayoutButton.addEventListener("click", () => {
      runLayout(runtime, {
        fitAfterLayout: true,
        resetPositions: true,
      });
    });
  }

  for (const input of dom.labelModeInputs) {
    if (!(input instanceof HTMLInputElement)) {
      continue;
    }
    input.addEventListener("change", () => {
      if (input.checked) {
        setLabelMode(runtime, input.value);
      }
    });
  }

  for (const input of dom.searchModeInputs) {
    if (!(input instanceof HTMLInputElement)) {
      continue;
    }
    input.addEventListener("change", () => {
      if (input.checked) {
        setSearchMode(runtime, normalizeSearchMode(input.value));
      }
    });
  }
};

const bindDisplayControls = (runtime: GraphRuntime): void => {
  const { dom, state } = runtime;
  updateSpreadLabel(dom.spreadInput, dom.spreadValue);
  if (dom.spreadInput instanceof HTMLInputElement) {
    dom.spreadInput.addEventListener("input", () => {
      updateSpreadLabel(dom.spreadInput, dom.spreadValue);
      runLayout(runtime, {
        fitAfterLayout: false,
        preserveViewport: true,
      });
    });
  }

  // The display controls deliberately bypass `publishFilterChange`: `g_isolate`,
  // `g_mindeg` and `g_collapse` describe what is drawn from the already-loaded
  // graph, so moving them must never cost a request.
  if (dom.isolateButton instanceof HTMLButtonElement) {
    dom.isolateButton.addEventListener("click", () => {
      if (state.selectedNodeID === "") {
        return;
      }
      state.display.isolateID = state.selectedNodeID;
      syncPageDisplayParams(state.display);
      rebuildDisplayForControl(runtime);
    });
  }

  if (dom.isolateClearButton instanceof HTMLButtonElement) {
    dom.isolateClearButton.addEventListener("click", () => {
      if (state.display.isolateID === "") {
        return;
      }
      state.display.isolateID = "";
      syncPageDisplayParams(state.display);
      rebuildDisplayForControl(runtime);
    });
  }

  if (dom.minDegreeInput instanceof HTMLInputElement) {
    const minDegreeInput = dom.minDegreeInput;
    minDegreeInput.addEventListener("input", () => {
      const minDegree = clampMinDegree(Number.parseInt(minDegreeInput.value, 10));
      if (minDegree === state.display.minDegree) {
        return;
      }
      state.display.minDegree = minDegree;
      // The URL and the label follow the slider immediately — only the
      // expensive part, the rebuild, waits for the drag to settle.
      syncPageDisplayParams(state.display);
      updateDisplayControls(runtime);
      scheduleDisplayRebuild(runtime, graphFilterDebounceMs);
    });
  }

  if (dom.collapseInput instanceof HTMLInputElement) {
    const collapseInput = dom.collapseInput;
    collapseInput.addEventListener("input", () => {
      const collapseDepth = clampCollapseDepth(Number.parseInt(collapseInput.value, 10));
      if (collapseDepth === state.display.collapseDepth) {
        return;
      }
      state.display.collapseDepth = collapseDepth;
      // Expansion keys are the first `collapseDepth` segments of a folder
      // path, so they mean something different at every depth. Carrying them
      // across would expand a group the user never opened.
      state.expandedFolders.clear();
      syncPageDisplayParams(state.display);
      updateDisplayControls(runtime);
      scheduleDisplayRebuild(runtime, graphFilterDebounceMs);
    });
  }

};

export const initGraph = (): void => {
  const dom = {
    canvas: getElementByID("graph-canvas", HTMLElement),
    status: getElementByID("graph-status", HTMLElement),
    detailsPanel: queryElement(document, ".graph-hover", HTMLElement),
    hoverTitle: getElementByID("graph-hover-title", HTMLElement),
    hoverFolder: getElementByID("graph-hover-folder", HTMLElement),
    hoverDegree: getElementByID("graph-hover-degree", HTMLElement),
    hoverIssues: getElementByID("graph-hover-issues", HTMLElement),
    openNote: getElementByID("graph-open-note", HTMLAnchorElement),
    filterInput: getElementByID("graph-filter-input", HTMLInputElement),
    spreadInput: getElementByID("graph-spread-input", HTMLInputElement),
    spreadValue: getElementByID("graph-spread-value", HTMLElement),
    isolateButton: getElementByID("graph-isolate-button", HTMLButtonElement),
    isolateClearButton: getElementByID("graph-isolate-clear-button", HTMLButtonElement),
    minDegreeInput: getElementByID("graph-mindegree-input", HTMLInputElement),
    minDegreeValue: getElementByID("graph-mindegree-value", HTMLElement),
    collapseInput: getElementByID("graph-collapse-input", HTMLInputElement),
    collapseValue: getElementByID("graph-collapse-value", HTMLElement),
    fitButton: getElementByID("graph-fit-button", HTMLButtonElement),
    zoomMatchesButton: getElementByID("graph-zoom-matches-button", HTMLButtonElement),
    resetLayoutButton: getElementByID("graph-reset-layout-button", HTMLButtonElement),
    labelModeInputs: queryAllElements(document, 'input[name="graph-label-mode"]', HTMLInputElement),
    searchModeInputs: queryAllElements(document, 'input[name="graph-search-mode"]', HTMLInputElement),
  };
  if (!dom.canvas) {
    return;
  }
  const detailsRefs: GraphDetailsRefs = {
    detailsPanel: dom.detailsPanel,
    hoverTitle: dom.hoverTitle,
    hoverFolder: dom.hoverFolder,
    hoverDegree: dom.hoverDegree,
    hoverIssues: dom.hoverIssues,
    openNote: dom.openNote,
  };

  const runtime = createGraphRuntime(dom, dom.canvas, detailsRefs);
  // The mount layer binds folder expansion before the display layer exists to
  // call into, so the runtime carries the hook rather than the two modules
  // importing each other.
  runtime.toggleFolderExpansion = (folder: string): void => {
    toggleFolderExpansion(runtime, folder);
  };

  const initialQuery = readPageQuery();
  const expression = document.querySelector<HTMLDetailsElement>(".graph-query-options");
  if (expression && new URLSearchParams(window.location.search).get("expr")) expression.open = true;
  const filterInput = dom.filterInput;
  if (filterInput instanceof HTMLInputElement) {
    filterInput.value = initialQuery;
  }
  for (const input of dom.searchModeInputs) {
    if (input instanceof HTMLInputElement) {
      input.checked = normalizeSearchMode(input.value) === runtime.state.searchMode;
    }
  }
  // The single refetch path. `mode: ""` — what the Library query bar publishes —
  // passes the gate, so an expression confirmed anywhere on the page reloads the
  // graph; `buildFilteredAPIURL` reads the URL, so the listener needs to know
  // nothing about the parameter that changed.
  window.addEventListener(filterChangeEventName, (event) => {
    if (!shouldHandleFilterChangeForMode(event, graphFilterMode)) {
      return;
    }
    const query =
      filterInput instanceof HTMLInputElement ? filterInput.value.trim() : runtime.state.currentQuery;
    scheduleLoad(runtime, query, 0);
  });
  // theme.ts dispatches bn:themechange on document (non-bubbling), so listen
  // there. Restyle in place without reloading graph data.
  document.addEventListener(THEME_EVENT, () => {
    restyleGraphForTheme(runtime);
  });

  // Runs before the first load so the graph is never fetched under a URL that
  // still promises a radius the page cannot honour.
  canonicalizeLegacyDisplayParams();
  bindControls(runtime);
  // The URL is authoritative for the display knobs at init, so a shared link
  // reproduces the view before the first payload lands.
  updateDisplayControls(runtime);
  scheduleLoad(runtime, initialQuery, 0);
};
