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
  currentGraphElements: GraphElements;
  hoveredNodeID: string;
  selectedNodeID: string;
  labelMode: GraphLabelMode;
  searchMode: GraphSearchMode;
  matchIDs: Set<string>;
  lastMeaningfulViewport: GraphViewportSnapshot | null;
  layoutRunID: number;
};

export type GraphSelectionOptions = {
  fromCytoscape?: boolean;
};

export type GraphLayoutRunOptions = {
  fitAfterLayout?: boolean;
  preserveViewport?: boolean;
  resetPositions?: boolean;
};
