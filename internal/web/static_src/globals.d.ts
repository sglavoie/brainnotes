import cytoscapeFactory = require("cytoscape");

declare global {
  interface Window {
    cytoscape: typeof cytoscapeFactory;
  }
}

export {};
