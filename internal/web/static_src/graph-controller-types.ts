import { type GraphDisplayOptions } from "./graph-controller-view";
import {
  type GraphElements,
  type GraphLabelMode,
  type GraphSearchMode,
  type GraphViewportSnapshot,
} from "./graph";

export type CytoscapeGraphOptions = {
  container: HTMLElement;
  elements: cytoscape.ElementDefinition[];
  style: cytoscape.StylesheetJson;
};

export type CytoscapeFactory = (options: CytoscapeGraphOptions) => cytoscape.Core;

export type GraphState = {
  cy: cytoscape.Core | null;
  activeLayout: cytoscape.Layouts | null;
  currentQuery: string;
  /** The fetched graph, verbatim: the source of truth for details and matches. */
  currentGraphElements: GraphElements;
  /** The display knobs; `expandedFolders`/`keepIDs` are recomputed per rebuild. */
  display: GraphDisplayOptions;
  /** What `buildDisplayGraph` produced from the source — the elements actually mounted. */
  displayGraphElements: GraphElements;
  /** Folders the user expanded back out of their collapse aggregate. */
  expandedFolders: Set<string>;
  hoveredNodeID: string;
  selectedNodeID: string;
  showSelectedNeighborLabels: boolean;
  labelMode: GraphLabelMode;
  searchMode: GraphSearchMode;
  matchIDs: Set<string>;
  lastMeaningfulViewport: GraphViewportSnapshot | null;
  layoutRunID: number;
};

export type GraphSelectionOptions = {
  fromCytoscape?: boolean;
  toggleNeighborLabels?: boolean;
};

export type GraphLayoutRunOptions = {
  fitAfterLayout?: boolean;
  preserveViewport?: boolean;
  resetPositions?: boolean;
};
