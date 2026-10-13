import { type GraphDetailsRefs } from "./graph-controller-details";
import { readLabelMode } from "./graph-controller-labels";
import { readPageDisplayParams, readPageSearchMode } from "./graph-controller-params";
import {
  type GraphDisplayOptions,
  type GraphDisplayStats,
  defaultGraphDisplayOptions,
  folderGroupKey,
} from "./graph-controller-view";
import { type GraphState } from "./graph-controller-types";
import {
  type GraphElements,
  type GraphNodeModel,
  emptyGraphElements,
} from "./graph";
import { type StatusKind } from "./status";

/** The page elements the graph controller drives. */
export type GraphDom = {
  canvas: HTMLElement | null;
  status: HTMLElement | null;
  detailsPanel: HTMLElement | null;
  hoverTitle: HTMLElement | null;
  hoverFolder: HTMLElement | null;
  hoverDegree: HTMLElement | null;
  hoverIssues: HTMLElement | null;
  openNote: HTMLAnchorElement | null;
  filterInput: HTMLInputElement | null;
  spreadInput: HTMLInputElement | null;
  spreadValue: HTMLElement | null;
  isolateButton: HTMLButtonElement | null;
  isolateClearButton: HTMLButtonElement | null;
  minDegreeInput: HTMLInputElement | null;
  minDegreeValue: HTMLElement | null;
  collapseInput: HTMLInputElement | null;
  collapseValue: HTMLElement | null;
  fitButton: HTMLButtonElement | null;
  zoomMatchesButton: HTMLButtonElement | null;
  resetLayoutButton: HTMLButtonElement | null;
  labelModeInputs: HTMLInputElement[];
  searchModeInputs: HTMLInputElement[];
};

/**
 * One page's graph controller: its elements, its graph state, and the mutable
 * scratch the modules below share.
 *
 * These fields were closure locals of a single `initGraph`, and splitting the
 * controller across modules is the only reason they are a record now. Each one
 * still has exactly one writer, named in its comment where that is not obvious.
 */
export type GraphRuntime = {
  dom: GraphDom;
  canvas: HTMLElement;
  detailsRefs: GraphDetailsRefs;
  state: GraphState;
  debounceToken: number;
  publishToken: number;
  /** A third debounce token, coalescing a slider drag over the display knobs. */
  displayToken: number;
  requestToken: number;
  selectionSyncing: boolean;
  /**
   * Cheap identity of the element set currently mounted in Cytoscape, taken over
   * the *displayed* elements: the fetched set is unchanged by a display knob, so
   * comparing it would read an isolate toggle as "nothing changed". Equal
   * signatures across a focus-mode reload mean the mounted graph did not change,
   * so match highlighting can be repainted without touching positions.
   */
  currentElementSignature: string;
  displayStats: GraphDisplayStats | null;
  /**
   * Match ids projected onto the displayed set: an aggregate matches when any of
   * its members does, so the mounted graph highlights what the source matched.
   */
  displayedMatchIDs: Set<string>;
  /**
   * What the load reported, kept so a display rebuild can re-render the line
   * without refetching. The reduction sentence is appended to it.
   */
  baseStatusMessage: string;
  baseStatusKind: StatusKind;
  /**
   * A one-off note about the display state itself, e.g. an isolate anchor that
   * the current query no longer contains.
   */
  displayNotice: string;
  /**
   * Set once by `initGraph`. `bindGraphEvents` needs folder expansion, which
   * belongs to the display module that mounts through it; routing the call
   * through the runtime is what keeps the mount layer below the display layer
   * instead of importing back into it.
   */
  toggleFolderExpansion: (folder: string) => void;
};

export const createGraphRuntime = (
  dom: GraphDom,
  canvas: HTMLElement,
  detailsRefs: GraphDetailsRefs
): GraphRuntime => ({
  dom,
  canvas,
  detailsRefs,
  state: {
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
    showSelectedNeighborLabels: false,
    labelMode: readLabelMode(dom.labelModeInputs),
    searchMode: readPageSearchMode(),
    matchIDs: new Set<string>(),
    lastMeaningfulViewport: null,
    layoutRunID: 0,
  },
  debounceToken: 0,
  publishToken: 0,
  displayToken: 0,
  requestToken: 0,
  selectionSyncing: false,
  currentElementSignature: "",
  displayStats: null,
  displayedMatchIDs: new Set<string>(),
  baseStatusMessage: "",
  baseStatusKind: "",
  displayNotice: "",
  toggleFolderExpansion: () => {},
});

/**
 * Vault-level metadata about a *fetched* node, displayed or not.
 *
 * Deliberately not an interactive lookup: the only caller is the isolation
 * status label, which names an anchor the pipeline may have thinned away. A
 * node that is not mounted cannot be selected, hovered, opened or expanded, so
 * nothing that drives interaction may reach past the displayed set.
 */
export const findSourceNodeModel = (
  runtime: GraphRuntime,
  nodeID: string
): GraphNodeModel | null =>
  runtime.state.currentGraphElements.nodes.find((node) => node.id === nodeID) || null;

/**
 * The one interactive source of truth: the elements currently mounted.
 *
 * `state.displayGraphElements` is exactly what `applyGraphElements` last put
 * into Cytoscape, so resolving here — with no fallback to the fetched source —
 * is what makes a node the display pipeline excluded unaddressable. It also
 * carries the recomputed degrees the details panel shows, and the aggregates
 * that have no entry in the fetched source at all.
 */
export const findDisplayedNodeModel = (
  runtime: GraphRuntime,
  nodeID: string
): GraphNodeModel | null => {
  if (nodeID === "") {
    return null;
  }
  return runtime.state.displayGraphElements.nodes.find((node) => node.id === nodeID) || null;
};

/** Whether an id arriving from Cytoscape still belongs to the mounted graph. */
export const isDisplayedNodeID = (runtime: GraphRuntime, nodeID: string): boolean =>
  findDisplayedNodeModel(runtime, nodeID) !== null;

/**
 * Projects the fetched match ids onto the mounted element set.
 *
 * Aggregation is the reason this is not just `state.matchIDs`: a collapsed
 * folder is a single mounted node whose members carry the matches, so it
 * highlights when any one of them does.
 */
export const computeDisplayedMatchIDs = (runtime: GraphRuntime): Set<string> => {
  const ids = new Set<string>();
  for (const node of runtime.state.displayGraphElements.nodes) {
    if (node.isAggregate) {
      if ((node.memberIDs || []).some((memberID) => runtime.state.matchIDs.has(memberID))) {
        ids.add(node.id);
      }
      continue;
    }
    if (runtime.state.matchIDs.has(node.id)) {
      ids.add(node.id);
    }
  }
  return ids;
};

export const elementSignature = (graphElements: GraphElements): string => {
  const nodeIDs = graphElements.nodes.map((node) => node.id).sort();
  const edgeIDs = graphElements.edges.map((edge) => edge.id).sort();
  return `${nodeIDs.join("\n")} ${edgeIDs.join("\n")}`;
};

export const matchIDsFor = (graphElements: GraphElements): Set<string> => {
  const ids = new Set<string>();
  for (const node of graphElements.nodes) {
    if (node.matchesQuery) {
      ids.add(node.id);
    }
  }
  return ids;
};

/** The options `buildDisplayGraph` runs with right now, keep-set included. */
export const currentDisplayOptions = (runtime: GraphRuntime): GraphDisplayOptions => {
  const keepIDs = new Set<string>();
  if (runtime.state.selectedNodeID !== "") {
    keepIDs.add(runtime.state.selectedNodeID);
  }
  if (runtime.state.display.isolateID !== "") {
    keepIDs.add(runtime.state.display.isolateID);
  }
  return {
    ...runtime.state.display,
    expandedFolders: runtime.state.expandedFolders,
    keepIDs,
  };
};

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
export const pruneExpandedFolders = (
  runtime: GraphRuntime,
  graphElements: GraphElements
): void => {
  if (runtime.state.expandedFolders.size === 0) {
    return;
  }
  const live = new Set<string>();
  for (const node of graphElements.nodes) {
    const key = folderGroupKey(node.folder, runtime.state.display.collapseDepth);
    if (key !== "") {
      live.add(key);
    }
  }
  for (const folder of Array.from(runtime.state.expandedFolders)) {
    if (!live.has(folder)) {
      runtime.state.expandedFolders.delete(folder);
    }
  }
};
