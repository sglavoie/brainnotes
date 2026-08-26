import cytoscapeFactory = require("cytoscape");

declare global {
  interface Window {
    cytoscape: typeof cytoscapeFactory;
    /**
     * The vendored `cytoscape-fcose` UMD bundle's registration function. fcose
     * ships no bundled types, and the script tag may be absent (or fail to
     * load), so this stays optional and unknown-shaped: every use site guards
     * before handing it to `cytoscape.use`.
     */
    cytoscapeFcose?: unknown;
  }
}

export {};
