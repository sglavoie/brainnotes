import { getElementByID, queryAllElements, queryElement } from "./dom";
import {
  buildFilteredAPIURL,
  dispatchFiltersChange,
  filterChangeEventName,
  hasActiveFilters,
  readPageQuery,
  shouldHandleFilterChangeForMode,
  syncPageQueryParam,
  updateBrowserQuery,
} from "./filters";
import { type GraphDetailsRefs, renderGraphDetails } from "./graph-controller-details";
import {
  applyGraphVisualClasses,
  normalizeSearchMode,
  readLabelMode,
} from "./graph-controller-labels";
import {
  applyViewport,
  captureNodePositions,
  clampedSpread,
  currentViewport,
  hasFcose,
  layoutOptionsForSpread,
  registerGraphLayouts,
  seedDeterministicPositions,
  separateIsolatedNodes,
  updateSpreadLabel,
} from "./graph-controller-layout";
import { buildGraphStyle } from "./graph-controller-style";
import {
  type GraphDisplayOptions,
  type GraphDisplayStats,
  buildDisplayGraph,
  defaultGraphDisplayOptions,
  folderGroupKey,
} from "./graph-controller-view";
import {
  type CytoscapeFactory,
  type GraphLayoutRunOptions,
  type GraphSelectionOptions,
  type GraphState,
} from "./graph-controller-types";
import {
  type GraphElements,
  type GraphLabelMode,
  type GraphNodeModel,
  type GraphSearchMode,
  type GraphViewportSnapshot,
  emptyGraphElements,
  graphNodeModelFromData,
  graphScopeLabel,
  normalizeGraphPayload,
  toCytoscapeElements,
} from "./graph";
import { type StatusKind, setStatusText } from "./status";
import { THEME_EVENT } from "./theme";

/**
 * The facet mode this page publishes and listens for. It matches
 * `graphFacetControlsData().Mode` on the server, so the graph's own facet chips
 * and its own controls speak the same mode; the query bar publishes `mode: ""`,
 * which reaches every mode.
 */
const graphFilterMode = "graph";

/** Debounce for a keystroke in the graph's free-text filter. */
const graphFilterDebounceMs = 150;

const readPageSearchMode = (): GraphSearchMode =>
  normalizeSearchMode(new URLSearchParams(window.location.search).get("q_mode"));

const syncPageSearchModeParam = (mode: GraphSearchMode): void => {
  const params = new URLSearchParams(window.location.search);
  if (mode === "focus") {
    params.set("q_mode", "focus");
  } else {
    params.delete("q_mode");
  }
  updateBrowserQuery(params);
};

/** Slider bound of the min-degree control. The pipeline itself has no upper clamp. */
const maxMinDegree = 10;

/** Folder-path depth bound of the collapse control, matching the pipeline's clamp. */
const maxCollapseDepth = 3;

const clampSliderInteger = (value: number, min: number, max: number): number =>
  Number.isFinite(value) ? Math.min(max, Math.max(min, Math.round(value))) : min;

export const clampMinDegree = (value: number): number =>
  clampSliderInteger(value, 0, maxMinDegree);

export const clampCollapseDepth = (value: number): number =>
  clampSliderInteger(value, 0, maxCollapseDepth);

/** The full display-knob set the URL carries. */
type GraphDisplayParams = {
  isolateID: string;
  minDegree: number;
  collapseDepth: number;
};

/** The isolation radius parameter this page used to carry, now retired. */
const legacyHopsParamKey = "g_hops";

// `g_isolate` / `g_mindeg` / `g_collapse` are display-only params, read and
// written exactly like `q_mode` above: never through `publishFilterChange`, and
// never copied by `buildFilteredAPIURL`, so moving them cannot refetch the
// graph.
const readPageDisplayParams = (): GraphDisplayParams => {
  const params = new URLSearchParams(window.location.search);
  const readInt = (key: string): number => Number.parseInt(params.get(key) || "", 10);
  return {
    isolateID: params.get("g_isolate") || "",
    minDegree: clampMinDegree(readInt("g_mindeg")),
    collapseDepth: clampCollapseDepth(readInt("g_collapse")),
  };
};

/**
 * Drops the retired isolation-radius parameter from a legacy link.
 *
 * Isolation is fixed at one hop, so a shared URL carrying a radius describes a
 * view this page can no longer render. Deleting it before the first load — not
 * merely ignoring it — is what keeps the address bar an honest description of
 * what is on screen, and `URLSearchParams.delete` removes every value under the
 * key, so a doubled `g_hops=2&g_hops=3` leaves nothing behind. Everything else
 * survives: `updateBrowserQuery` rebuilds the URL from the current pathname, the
 * remaining parameters, and the current hash.
 */
const canonicalizeLegacyDisplayParams = (): void => {
  const params = new URLSearchParams(window.location.search);
  if (!params.has(legacyHopsParamKey)) {
    return;
  }
  params.delete(legacyHopsParamKey);
  updateBrowserQuery(params);
};

/**
 * Writes the whole display-knob set into the URL at once.
 *
 * Every knob is omitted at its off value, so a default view keeps a clean,
 * shareable URL and only the knobs the user actually moved show up in it.
 */
const syncPageDisplayParams = (display: GraphDisplayParams): void => {
  const params = new URLSearchParams(window.location.search);
  const write = (key: string, value: string, offValue: string): void => {
    if (value === offValue) {
      params.delete(key);
      return;
    }
    params.set(key, value);
  };
  write("g_isolate", display.isolateID, "");
  write("g_mindeg", String(display.minDegree), "0");
  write("g_collapse", String(display.collapseDepth), "0");
  // Defensive, not merely tidy: this is the one writer of the display knobs, so
  // deleting the retired radius here means no path can put it back into a URL
  // the user is about to share.
  params.delete(legacyHopsParamKey);
  updateBrowserQuery(params);
};

export const formatMinDegreeLabel = (minDegree: number): string =>
  minDegree <= 0 ? "off" : `≥ ${minDegree}`;

export const formatCollapseDepthLabel = (depth: number): string =>
  depth <= 0 ? "off" : `depth ${depth}`;

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
  const canvas = dom.canvas;
  const detailsRefs: GraphDetailsRefs = {
    detailsPanel: dom.detailsPanel,
    hoverTitle: dom.hoverTitle,
    hoverFolder: dom.hoverFolder,
    hoverDegree: dom.hoverDegree,
    hoverIssues: dom.hoverIssues,
    openNote: dom.openNote,
  };

  const state: GraphState = {
    cy: null,
    activeLayout: null,
    currentQuery: "",
    currentGraphElements: emptyGraphElements(),
    display: {
      ...defaultGraphDisplayOptions(),
      ...readPageDisplayParams(),
    },
    displayGraphElements: emptyGraphElements(),
    expandedFolders: new Set<string>(),
    hoveredNodeID: "",
    selectedNodeID: "",
    labelMode: readLabelMode(dom.labelModeInputs),
    searchMode: readPageSearchMode(),
    matchIDs: new Set<string>(),
    lastMeaningfulViewport: null,
    layoutRunID: 0,
  };
  let debounceToken = 0;
  let publishToken = 0;
  // A third debounce token, the same closure-scoped shape as the two above. This
  // one coalesces a slider drag over the display knobs, which drive a relayout
  // rather than a fetch.
  let displayToken = 0;
  let requestToken = 0;
  let selectionSyncing = false;
  // Equal signatures across a focus-mode reload mean the mounted graph did not
  // change, so we can repaint match highlighting without touching positions.
  // Cheap identity of the element set currently mounted in Cytoscape, taken over
  // the *displayed* elements: the fetched set is unchanged by a display knob, so
  // comparing it would read an isolate toggle as "nothing changed".
  let currentElementSignature = "";
  let displayStats: GraphDisplayStats | null = null;
  // Match ids projected onto the displayed set: an aggregate matches when any of
  // its members does, so the mounted graph highlights what the source matched.
  let displayedMatchIDs = new Set<string>();
  // What the load reported, kept so a display rebuild can re-render the line
  // without refetching. The reduction sentence is appended to it.
  let baseStatusMessage = "";
  let baseStatusKind: StatusKind = "";
  // A one-off note about the display state itself, e.g. an isolate anchor that
  // the current query no longer contains.
  let displayNotice = "";

  function setStatus(message: string, kind: StatusKind = ""): void {
    baseStatusMessage = message;
    baseStatusKind = kind;
    renderStatus();
  }

  /**
   * The explicit account of what the display pipeline removed.
   *
   * Silent thinning is the failure mode this exists to prevent: whenever fewer
   * nodes are drawn than were fetched, the status line says so and names the
   * knob responsible.
   */
  function displayReductionSentence(): string {
    if (!displayStats || displayStats.displayedNodeCount >= displayStats.sourceNodeCount) {
      return "";
    }
    const counts = `Showing ${displayStats.displayedNodeCount} of ${displayStats.sourceNodeCount} nodes`;
    const reasons: string[] = [];
    if (displayStats.isolated) {
      const anchor = findSourceNodeModel(state.display.isolateID);
      const anchorLabel = anchor ? anchor.title || anchor.id : state.display.isolateID;
      // The radius is fixed, but the sentence still names it: "isolated from X"
      // would leave the user guessing how much of the neighborhood is on screen.
      reasons.push(`isolated 1 hop from "${anchorLabel}"`);
    }
    if (displayStats.droppedByDegree > 0) {
      reasons.push(`degree ≥ ${state.display.minDegree}`);
    }
    if (displayStats.aggregateCount > 0) {
      reasons.push(`folders collapsed to depth ${state.display.collapseDepth}`);
    }
    return reasons.length === 0 ? `${counts}.` : `${counts} — ${reasons.join(", ")}.`;
  }

  function renderStatus(): void {
    const message = [baseStatusMessage, displayReductionSentence(), displayNotice]
      .filter((part) => part !== "")
      .join(" ");
    setStatusText(dom.status, message, baseStatusKind, ["is-loading", "is-empty", "is-error"]);
  }

  function renderDetailsPanel(fallbackHoveredModel: GraphNodeModel | null = null): void {
    const selectedModel = findDisplayedNodeModel(state.selectedNodeID);
    const hoveredModel = selectedModel
      ? null
      : findDisplayedNodeModel(state.hoveredNodeID) || fallbackHoveredModel || null;
    renderGraphDetails(detailsRefs, selectedModel, hoveredModel);
  }

  function setLabelMode(nextMode: string): void {
    state.labelMode = nextMode === "all" ? "all" : "auto";
    applyGraphVisualState();
  }

  function setSearchMode(nextMode: GraphSearchMode): void {
    if (state.searchMode === nextMode) {
      return;
    }
    state.searchMode = nextMode;
    syncPageSearchModeParam(nextMode);
    // Toggling changes the included set, so this reload deliberately goes
    // through the normal relayout path.
    publishFilterChange(0);
  }

  function restyleGraphForTheme(): void {
    if (!state.cy) {
      return;
    }
    // Replacing the stylesheet repaints colors only; graph data, selection,
    // viewport, and layout positions are untouched. Re-toggle visual classes so
    // label/focus state stays in sync with the freshly applied style.
    state.cy.style(buildGraphStyle());
    applyGraphVisualState();
  }

  function stopActiveLayout(): void {
    if (state.activeLayout && typeof state.activeLayout.stop === "function") {
      state.activeLayout.stop();
    }
    state.activeLayout = null;
  }

  function fitCurrentGraph(): void {
    if (!state.cy || state.cy.nodes().length === 0) {
      return;
    }
    state.cy.fit(state.cy.elements(), 72);
    rememberMeaningfulViewport();
  }

  function rememberMeaningfulViewport(): void {
    if (!state.cy || state.cy.nodes().length === 0) {
      return;
    }
    state.lastMeaningfulViewport = currentViewport(state.cy);
  }

  function ensureGraph(): cytoscape.Core {
    if (state.cy) {
      return state.cy;
    }
    registerGraphLayouts();
    const createCytoscape = window.cytoscape as CytoscapeFactory;
    state.cy = createCytoscape({
      container: canvas,
      elements: [],
      style: buildGraphStyle(),
    });
    bindGraphEvents(state.cy);
    return state.cy;
  }

  /**
   * Node events are only honoured for elements the display pipeline kept.
   *
   * Cytoscape can deliver a tap, select or mouseover for an element a remount
   * already removed — a queued gesture, or a pointer that was over an excluded
   * node when the rebuild ran. Such an event names a node the user can no longer
   * see, so it must not move selection, details, the display controls or the
   * isolation state.
   */
  function bindGraphEvents(instance: cytoscape.Core): void {
    instance.on("tap", "node", (event: cytoscape.EventObjectNode) => {
      const nodeID = event.target.id();
      const model = findDisplayedNodeModel(nodeID);
      if (!model) {
        return;
      }
      if (model.isAggregate) {
        // A collapsed folder is a drill-down affordance, not a note: tapping it
        // expands the group back out rather than selecting a synthetic node.
        toggleFolderExpansion(model.folder);
        return;
      }
      selectNode(nodeID);
    });

    instance.on("tap", (event: cytoscape.EventObjectCore) => {
      if (event.target === instance) {
        clearSelectedNode();
      }
    });

    instance.on("mouseover", "node", (event: cytoscape.EventObjectNode) => {
      const nodeID = event.target.id();
      if (!isDisplayedNodeID(nodeID)) {
        return;
      }
      const data = event.target.data();
      canvas.classList.add("is-node-hovered");
      setHoveredNode(nodeID, data);
    });
    instance.on("mouseout", "node", () => {
      canvas.classList.remove("is-node-hovered");
      setHoveredNode("", null);
    });
    instance.on("select", "node", (event: cytoscape.EventObjectNode) => {
      if (!selectionSyncing) {
        selectNode(event.target.id(), { fromCytoscape: true });
      }
    });
    instance.on("unselect", "node", (event: cytoscape.EventObjectNode) => {
      const nodeID = event.target.id();
      if (!selectionSyncing && isDisplayedNodeID(nodeID) && nodeID === state.selectedNodeID) {
        clearSelectedNode({ fromCytoscape: true });
      }
    });
    instance.on("viewport", () => {
      rememberMeaningfulViewport();
      applyGraphVisualState();
    });
  }

  /**
   * Vault-level metadata about a *fetched* node, displayed or not.
   *
   * Deliberately not an interactive lookup: the only caller is the isolation
   * status label, which names an anchor the pipeline may have thinned away. A
   * node that is not mounted cannot be selected, hovered, opened or expanded, so
   * nothing that drives interaction may reach past the displayed set.
   */
  function findSourceNodeModel(nodeID: string): GraphNodeModel | null {
    return state.currentGraphElements.nodes.find((node) => node.id === nodeID) || null;
  }

  /**
   * The one interactive source of truth: the elements currently mounted.
   *
   * `state.displayGraphElements` is exactly what `applyGraphElements` last put
   * into Cytoscape, so resolving here — with no fallback to the fetched source —
   * is what makes a node the display pipeline excluded unaddressable. It also
   * carries the recomputed degrees the details panel shows, and the aggregates
   * that have no entry in the fetched source at all.
   */
  function findDisplayedNodeModel(nodeID: string): GraphNodeModel | null {
    if (nodeID === "") {
      return null;
    }
    return state.displayGraphElements.nodes.find((node) => node.id === nodeID) || null;
  }

  /** Whether an id arriving from Cytoscape still belongs to the mounted graph. */
  function isDisplayedNodeID(nodeID: string): boolean {
    return findDisplayedNodeModel(nodeID) !== null;
  }

  function syncCytoscapeSelection(nodeID: string): void {
    if (!state.cy || selectionSyncing) {
      return;
    }

    selectionSyncing = true;
    try {
      state.cy.nodes().forEach((node) => {
        const shouldSelect = node.id() === nodeID;
        if (shouldSelect && !node.selected()) {
          node.select();
        } else if (!shouldSelect && node.selected()) {
          node.unselect();
        }
      });
    } finally {
      selectionSyncing = false;
    }
  }

  function selectNode(nodeID: string, options: GraphSelectionOptions = {}): void {
    const model = findDisplayedNodeModel(nodeID);
    if (!model) {
      // Nothing mounted answers to this id, so there is no selection to make and
      // no reason to disturb the one already in place.
      return;
    }
    // An aggregate is never a selection: it stands for a group, and every
    // selection-driven affordance (Open note, Isolate) means one note.
    state.selectedNodeID = model.isAggregate ? "" : model.id;
    if (!options || !options.fromCytoscape) {
      syncCytoscapeSelection(state.selectedNodeID);
    }
    renderDetailsPanel();
    applyGraphVisualState();
    updateDisplayControls();
  }

  function clearSelectedNode(options: GraphSelectionOptions = {}): void {
    state.selectedNodeID = "";
    if (!options || !options.fromCytoscape) {
      syncCytoscapeSelection("");
    }
    renderDetailsPanel();
    applyGraphVisualState();
    updateDisplayControls();
  }

  function setHoveredNode(nodeID: string, fallbackData: unknown): void {
    state.hoveredNodeID = nodeID || "";
    if (!state.hoveredNodeID) {
      renderDetailsPanel();
      applyGraphVisualState();
      return;
    }

    const fallbackModel = fallbackData
      ? graphNodeModelFromData(fallbackData, state.hoveredNodeID)
      : null;
    renderDetailsPanel(fallbackModel);
    applyGraphVisualState();
  }

  function keyboardTargetNodeID(): string {
    if (isDisplayedNodeID(state.hoveredNodeID)) {
      return state.hoveredNodeID;
    }
    if (isDisplayedNodeID(state.selectedNodeID)) {
      return state.selectedNodeID;
    }
    if (!state.cy) {
      return "";
    }

    const selectedNode = state.cy
      .nodes()
      .filter((node) => node.selected())
      .first();
    if (!selectedNode || selectedNode.length === 0) {
      return "";
    }
    // Cytoscape's own selection is the last resort, and it is checked against the
    // displayed set like every other id: a keyboard activation must not reach an
    // element the pipeline excluded.
    const nodeID = selectedNode.id();
    return isDisplayedNodeID(nodeID) ? nodeID : "";
  }

  function handleGraphKeyDown(event: KeyboardEvent): void {
    if (event.key !== "Enter" && event.key !== " " && event.key !== "Spacebar") {
      return;
    }

    const nodeID = keyboardTargetNodeID();
    if (!nodeID) {
      return;
    }

    event.preventDefault();
    selectNode(nodeID);
  }

  /**
   * Projects the fetched match ids onto the mounted element set.
   *
   * Aggregation is the reason this is not just `state.matchIDs`: a collapsed
   * folder is a single mounted node whose members carry the matches, so it
   * highlights when any one of them does.
   */
  function computeDisplayedMatchIDs(): Set<string> {
    const ids = new Set<string>();
    for (const node of state.displayGraphElements.nodes) {
      if (node.isAggregate) {
        if ((node.memberIDs || []).some((memberID) => state.matchIDs.has(memberID))) {
          ids.add(node.id);
        }
        continue;
      }
      if (state.matchIDs.has(node.id)) {
        ids.add(node.id);
      }
    }
    return ids;
  }

  function applyGraphVisualState(): void {
    applyGraphVisualClasses(
      state.cy,
      state.labelMode,
      state.selectedNodeID,
      state.hoveredNodeID,
      displayedMatchIDs
    );
  }

  function updateZoomMatchesButton(): void {
    if (dom.zoomMatchesButton instanceof HTMLButtonElement) {
      dom.zoomMatchesButton.disabled = displayedMatchIDs.size === 0;
    }
  }

  /** Disabled-state of the display controls, refreshed on every rebuild and selection. */
  function updateDisplayControls(): void {
    if (dom.isolateButton instanceof HTMLButtonElement) {
      dom.isolateButton.disabled =
        state.selectedNodeID === "" || state.selectedNodeID === state.display.isolateID;
    }
    if (dom.isolateClearButton instanceof HTMLButtonElement) {
      dom.isolateClearButton.disabled = state.display.isolateID === "";
    }
    if (dom.minDegreeInput instanceof HTMLInputElement) {
      dom.minDegreeInput.value = String(state.display.minDegree);
    }
    if (dom.minDegreeValue) {
      dom.minDegreeValue.textContent = formatMinDegreeLabel(state.display.minDegree);
    }
    if (dom.collapseInput instanceof HTMLInputElement) {
      dom.collapseInput.value = String(state.display.collapseDepth);
    }
    if (dom.collapseValue) {
      dom.collapseValue.textContent = formatCollapseDepthLabel(state.display.collapseDepth);
    }
  }

  function zoomToMatches(): void {
    if (!state.cy || displayedMatchIDs.size === 0) {
      return;
    }
    const matchedNodes = state.cy.nodes().filter((node) => displayedMatchIDs.has(node.id()));
    if (matchedNodes.length === 0) {
      return;
    }
    state.cy.fit(matchedNodes, 96);
    rememberMeaningfulViewport();
  }

  function runLayout(options: GraphLayoutRunOptions = {}): void {
    if (!state.cy || state.cy.nodes().length === 0) {
      return;
    }
    const layoutOptions = options;
    const fitAfterLayout = Boolean(layoutOptions.fitAfterLayout);
    const preservedViewport = layoutOptions.preserveViewport ? currentViewport(state.cy) : null;
    const runID = state.layoutRunID + 1;
    state.layoutRunID = runID;
    stopActiveLayout();

    if (layoutOptions.resetPositions) {
      seedDeterministicPositions(state.cy, clampedSpread(dom.spreadInput));
    }

    let finalized = false;
    const finalizeLayout = () => {
      if (finalized || runID !== state.layoutRunID) {
        return;
      }
      finalized = true;
      // fcose's `tile: true` already parks zero-degree nodes; running both
      // fights the layout. The helper stays as the cose fallback path.
      if (!hasFcose()) {
        separateIsolatedNodes(state.cy, clampedSpread(dom.spreadInput));
      }
      if (fitAfterLayout) {
        fitCurrentGraph();
      } else if (preservedViewport) {
        applyViewport(state.cy, preservedViewport);
        rememberMeaningfulViewport();
      } else {
        rememberMeaningfulViewport();
      }
      applyGraphVisualState();
      state.activeLayout = null;
    };

    const layout = state.cy.layout(layoutOptionsForSpread(clampedSpread(dom.spreadInput)));
    state.activeLayout = layout;
    if (typeof layout.one === "function") {
      layout.one("layoutstop", finalizeLayout);
    }
    layout.run();
    if (typeof layout.one !== "function") {
      finalizeLayout();
    }
  }

  function applyGraphElements(
    graphElements: GraphElements,
    layoutOptions: GraphLayoutRunOptions
  ): void {
    const instance = ensureGraph();
    stopActiveLayout();
    const selectedNodeID = state.selectedNodeID;
    // Remounting throws every position away. A display-knob rebuild asks for
    // `resetPositions: false`, which here means "carry the surviving nodes'
    // positions across", so isolating does not shuffle the neighborhood the user
    // was already looking at.
    const rememberedPositions = layoutOptions.resetPositions ? null : captureNodePositions(instance);

    selectionSyncing = true;
    try {
      instance.elements().remove();
      if (graphElements.nodes.length > 0 || graphElements.edges.length > 0) {
        instance.add(toCytoscapeElements(graphElements));
      }
    } finally {
      selectionSyncing = false;
    }

    state.selectedNodeID = graphElements.nodes.some((node) => node.id === selectedNodeID) ? selectedNodeID : "";
    syncCytoscapeSelection(state.selectedNodeID);
    setHoveredNode("", null);

    if (instance.nodes().length === 0) {
      state.lastMeaningfulViewport = null;
      applyGraphVisualState();
      return;
    }

    if (rememberedPositions) {
      seedDeterministicPositions(instance, clampedSpread(dom.spreadInput), rememberedPositions);
    }
    runLayout(layoutOptions);
  }

  /** The options `buildDisplayGraph` runs with right now, keep-set included. */
  function currentDisplayOptions(): GraphDisplayOptions {
    const keepIDs = new Set<string>();
    if (state.selectedNodeID !== "") {
      keepIDs.add(state.selectedNodeID);
    }
    if (state.display.isolateID !== "") {
      keepIDs.add(state.display.isolateID);
    }
    return {
      ...state.display,
      expandedFolders: state.expandedFolders,
      keepIDs,
    };
  }

  /**
   * Runs the display pipeline over the fetched graph and mounts the result.
   *
   * The one place display state becomes pixels: every knob goes through here, and
   * nothing here refetches.
   */
  function rebuildDisplay(
    layoutOptions: GraphLayoutRunOptions,
    prebuilt: { elements: GraphElements; stats: GraphDisplayStats } | null = null
  ): void {
    const built = prebuilt || buildDisplayGraph(state.currentGraphElements, currentDisplayOptions());

    // A query change can filter the anchor out from under a live isolation. The
    // pipeline treats that as a no-op, so without this the graph would silently
    // spring back to full while the URL and the clear button still claimed an
    // isolated view.
    if (state.display.isolateID !== "" && !built.stats.isolated) {
      const droppedAnchor = state.display.isolateID;
      state.display.isolateID = "";
      syncPageDisplayParams(state.display);
      displayNotice = `Isolation cleared: "${droppedAnchor}" is not in the current graph.`;
    } else {
      displayNotice = "";
    }

    state.displayGraphElements = built.elements;
    displayStats = built.stats;
    displayedMatchIDs = computeDisplayedMatchIDs();
    currentElementSignature = elementSignature(built.elements);
    applyGraphElements(built.elements, layoutOptions);
    updateZoomMatchesButton();
    updateDisplayControls();
    renderStatus();
  }

  /** A display-knob rebuild: relayout in place, never yanking the viewport. */
  function rebuildDisplayForControl(): void {
    rebuildDisplay({
      fitAfterLayout: false,
      preserveViewport: true,
      resetPositions: false,
    });
  }

  /**
   * A display-knob rebuild, coalesced across a slider drag.
   *
   * Unlike the free-text filter — where the debounce guards a fetch that a
   * request token then makes safe to lose — a rebuild is a synchronous remount
   * plus a relayout, so a 0-to-10 min-degree sweep would otherwise queue eleven
   * of them. Two guards make the drag cost one: this debounce collapses the
   * burst into a single trailing rebuild, and the in-flight layout belonging to
   * a slider position the user has already moved past is stopped right away.
   * `runLayout`'s `layoutRunID` bump is the backstop — it discards a stale
   * `layoutstop` if the engine fires one after the stop anyway.
   */
  function scheduleDisplayRebuild(delay: number): void {
    window.clearTimeout(displayToken);
    if (delay <= 0) {
      rebuildDisplayForControl();
      return;
    }
    stopActiveLayout();
    displayToken = window.setTimeout(() => {
      rebuildDisplayForControl();
    }, delay);
  }

  /**
   * Drops expansion keys the freshly fetched graph can no longer produce.
   *
   * Expansion is deliberately sticky across a refetch — a keystroke in focus
   * mode must not re-collapse a folder the user just opened — so the set is
   * pruned rather than cleared. Without the prune it only ever grows, and a
   * folder that a narrowing query removed would silently come back
   * pre-expanded the moment the query widened again. A depth change is the
   * other trigger, and that one clears outright: the keys are depth-specific.
   */
  function pruneExpandedFolders(graphElements: GraphElements): void {
    if (state.expandedFolders.size === 0) {
      return;
    }
    const live = new Set<string>();
    for (const node of graphElements.nodes) {
      const key = folderGroupKey(node.folder, state.display.collapseDepth);
      if (key !== "") {
        live.add(key);
      }
    }
    for (const folder of Array.from(state.expandedFolders)) {
      if (!live.has(folder)) {
        state.expandedFolders.delete(folder);
      }
    }
  }

  function toggleFolderExpansion(folder: string): void {
    if (folder === "") {
      return;
    }
    if (state.expandedFolders.has(folder)) {
      state.expandedFolders.delete(folder);
    } else {
      state.expandedFolders.add(folder);
    }
    rebuildDisplayForControl();
  }

  function buildQueryURL(query: string): string {
    const url = buildFilteredAPIURL("/api/graph.json", query);
    if (state.searchMode !== "focus") {
      return url;
    }
    const parsed = new URL(url, window.location.origin);
    parsed.searchParams.set("q_mode", "focus");
    return `${parsed.pathname}${parsed.search}`;
  }

  function elementSignature(graphElements: GraphElements): string {
    const nodeIDs = graphElements.nodes.map((node) => node.id).sort();
    const edgeIDs = graphElements.edges.map((edge) => edge.id).sort();
    return `${nodeIDs.join("\n")} ${edgeIDs.join("\n")}`;
  }

  function matchIDsFor(graphElements: GraphElements): Set<string> {
    const ids = new Set<string>();
    for (const node of graphElements.nodes) {
      if (node.matchesQuery) {
        ids.add(node.id);
      }
    }
    return ids;
  }

  // Repaints match highlighting on the mounted graph without rebuilding it, so
  // positions, zoom, pan and selection survive a keystroke in focus mode.
  function refreshMatchHighlighting(
    graphElements: GraphElements,
    displayElements: GraphElements
  ): void {
    const instance = state.cy;
    if (!instance) {
      return;
    }
    state.currentGraphElements = graphElements;
    state.displayGraphElements = displayElements;
    // The mounted ids are display ids, so the match set has to be projected
    // through the aggregation before it can be written onto nodes.
    displayedMatchIDs = computeDisplayedMatchIDs();
    instance.batch(() => {
      instance.nodes().forEach((node) => {
        node.data("matchesQuery", displayedMatchIDs.has(node.id()));
      });
    });
    renderDetailsPanel();
    applyGraphVisualState();
  }

  async function loadGraph(query: string, localToken: number): Promise<void> {
    if (typeof window.cytoscape !== "function") {
      setStatus("Graph library failed to load.", "is-error");
      return;
    }

    setStatus("Loading graph...", "is-loading");

    let payload: unknown;
    try {
      const resp = await fetch(buildQueryURL(query), {
        headers: { Accept: "application/json" },
      });
      if (localToken !== requestToken) {
        return;
      }
      if (!resp.ok) {
        setStatus(`Graph load failed (${resp.status}).`, "is-error");
        return;
      }
      payload = await resp.json();
    } catch (error) {
      if (localToken !== requestToken) {
        return;
      }
      setStatus("Graph load failed.", "is-error");
      return;
    }

    if (localToken !== requestToken) {
      return;
    }

    const graphElements = normalizeGraphPayload(payload);
    const focusMode = state.searchMode === "focus";
    state.matchIDs = matchIDsFor(graphElements);
    // Before anything is built from the new payload, so the signature compared
    // below and the graph finally mounted run with the same expansion set.
    pruneExpandedFolders(graphElements);
    // The comparison is over what the new payload *would* mount, not over the
    // payload itself: display knobs sit between the two, so only the displayed
    // signature can answer "is the mounted graph the same graph?".
    const built = buildDisplayGraph(graphElements, currentDisplayOptions());
    const signature = elementSignature(built.elements);

    if (focusMode && state.cy && signature === currentElementSignature) {
      // Same nodes and edges as the mounted graph: only the match set moved, so
      // repaint highlighting in place instead of relaying out under the cursor.
      refreshMatchHighlighting(graphElements, built.elements);
      displayStats = built.stats;
    } else {
      state.currentGraphElements = graphElements;
      rebuildDisplay(
        {
          fitAfterLayout: true,
          resetPositions: true,
        },
        built
      );
    }
    updateZoomMatchesButton();

    // The expression counts as an active filter here: a graph narrowed only by
    // `expr=` would otherwise report an unscoped node count.
    const hasFilters = hasActiveFilters();
    const scopeLabel = graphScopeLabel(query, hasFilters);

    if (focusMode && query) {
      if (state.matchIDs.size === 0) {
        // The graph stays on screen in focus mode; only the highlighting is empty.
        setStatus(`No notes match ${scopeLabel}.`, "is-empty");
        return;
      }
      setStatus(
        `Focusing ${state.matchIDs.size} of ${graphElements.nodes.length} nodes matching ${scopeLabel}.`
      );
      return;
    }

    if (scopeLabel) {
      if (graphElements.nodes.length === 0) {
        setStatus(`No notes match ${scopeLabel}.`, "is-empty");
        return;
      }
      setStatus(
        `Showing ${graphElements.nodes.length} nodes and ${graphElements.edges.length} edges for ${scopeLabel}.`
      );
      return;
    }

    setStatus(`Loaded ${graphElements.nodes.length} nodes and ${graphElements.edges.length} edges.`);
  }

  function scheduleLoad(query: string, delay: number): void {
    window.clearTimeout(debounceToken);
    state.currentQuery = query;
    const localToken = ++requestToken;
    if (delay <= 0) {
      void loadGraph(query, localToken);
      return;
    }
    debounceToken = window.setTimeout(() => {
      void loadGraph(query, localToken);
    }, delay);
  }

  /**
   * Announces that this page's filter state moved.
   *
   * The graph's own controls publish rather than reloading directly, so every
   * refetch — the free-text filter, the search mode, the facet chips, and the
   * Library query bar — arrives through the one listener below. The debounce
   * lives on the publish rather than on the load so a burst of keystrokes emits
   * one event instead of one per character.
   */
  function publishFilterChange(delay: number): void {
    window.clearTimeout(publishToken);
    const emit = (): void => {
      dispatchFiltersChange(window, {
        mode: graphFilterMode,
        params: new URLSearchParams(window.location.search),
      });
    };
    if (delay <= 0) {
      emit();
      return;
    }
    publishToken = window.setTimeout(emit, delay);
  }

  function bindControls(): void {
    canvas.addEventListener("keydown", handleGraphKeyDown);

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
      filterInput.addEventListener("input", () => {
        syncPageQueryParam(filterInput.value.trim());
        publishFilterChange(graphFilterDebounceMs);
      });
      filterInput.addEventListener("keydown", (event) => {
        if (event.key !== "Enter") {
          return;
        }
        event.preventDefault();
        zoomToMatches();
      });
    }

    updateSpreadLabel(dom.spreadInput, dom.spreadValue);
    if (dom.spreadInput instanceof HTMLInputElement) {
      dom.spreadInput.addEventListener("input", () => {
        updateSpreadLabel(dom.spreadInput, dom.spreadValue);
        runLayout({
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
        rebuildDisplayForControl();
      });
    }

    if (dom.isolateClearButton instanceof HTMLButtonElement) {
      dom.isolateClearButton.addEventListener("click", () => {
        if (state.display.isolateID === "") {
          return;
        }
        state.display.isolateID = "";
        syncPageDisplayParams(state.display);
        rebuildDisplayForControl();
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
        updateDisplayControls();
        scheduleDisplayRebuild(graphFilterDebounceMs);
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
        updateDisplayControls();
        scheduleDisplayRebuild(graphFilterDebounceMs);
      });
    }

    if (dom.fitButton instanceof HTMLButtonElement) {
      dom.fitButton.addEventListener("click", fitCurrentGraph);
    }

    if (dom.zoomMatchesButton instanceof HTMLButtonElement) {
      dom.zoomMatchesButton.addEventListener("click", zoomToMatches);
    }
    updateZoomMatchesButton();

    if (dom.resetLayoutButton instanceof HTMLButtonElement) {
      dom.resetLayoutButton.addEventListener("click", () => {
        runLayout({
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
          setLabelMode(input.value);
        }
      });
    }

    for (const input of dom.searchModeInputs) {
      if (!(input instanceof HTMLInputElement)) {
        continue;
      }
      input.addEventListener("change", () => {
        if (input.checked) {
          setSearchMode(normalizeSearchMode(input.value));
        }
      });
    }
  }

  const initialQuery = readPageQuery();
  const filterInput = dom.filterInput;
  if (filterInput instanceof HTMLInputElement) {
    filterInput.value = initialQuery;
  }
  for (const input of dom.searchModeInputs) {
    if (input instanceof HTMLInputElement) {
      input.checked = normalizeSearchMode(input.value) === state.searchMode;
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
    const query = filterInput instanceof HTMLInputElement ? filterInput.value.trim() : state.currentQuery;
    scheduleLoad(query, 0);
  });
  // theme.ts dispatches bn:themechange on document (non-bubbling), so listen
  // there. Restyle in place without reloading graph data.
  document.addEventListener(THEME_EVENT, restyleGraphForTheme);

  // Runs before the first load so the graph is never fetched under a URL that
  // still promises a radius the page cannot honour.
  canonicalizeLegacyDisplayParams();
  bindControls();
  // The URL is authoritative for the display knobs at init, so a shared link
  // reproduces the view before the first payload lands.
  updateDisplayControls();
  scheduleLoad(initialQuery, 0);
};
