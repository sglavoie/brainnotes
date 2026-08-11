import {
  DEFAULT_GRAPH_PALETTE,
  type GraphPalette,
  buildGraphStyleRules,
  readGraphPalette,
} from "./graph";

export const currentGraphPalette = (): GraphPalette => {
  if (typeof window === "undefined" || typeof document === "undefined") {
    return DEFAULT_GRAPH_PALETTE;
  }
  const root = document.documentElement;
  if (!root || typeof window.getComputedStyle !== "function") {
    return DEFAULT_GRAPH_PALETTE;
  }
  const styles = window.getComputedStyle(root);
  return readGraphPalette((name) => styles.getPropertyValue(name));
};

export const buildGraphStyle = (): cytoscape.StylesheetJson =>
  buildGraphStyleRules(currentGraphPalette()) as unknown as cytoscape.StylesheetJson;
