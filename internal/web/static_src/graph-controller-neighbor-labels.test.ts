// @vitest-environment jsdom

import cytoscape from "cytoscape";
import { afterEach, describe, expect, it } from "vitest";

import { handleGraphKeyDown } from "./graph-controller-interaction";
import { applyGraphElements, bindGraphEvents } from "./graph-controller-mount";
import { createGraphRuntime } from "./graph-controller-runtime";
import { emptyGraphElements, normalizeGraphPayload, toCytoscapeElements } from "./graph";

const instances: cytoscape.Core[] = [];
afterEach(() => instances.splice(0).forEach((cy) => cy.destroy()));

const mountGraph = () => {
  const canvas = document.createElement("div");
  const details = {
    detailsPanel: null, hoverTitle: null, hoverFolder: null,
    hoverDegree: null, hoverIssues: null, openNote: null,
  };
  const runtime = createGraphRuntime({
    ...details,
    canvas, status: null, filterInput: null, spreadInput: null, spreadValue: null,
    isolateButton: null, isolateClearButton: null, minDegreeInput: null,
    minDegreeValue: null, collapseInput: null, collapseValue: null, fitButton: null,
    zoomMatchesButton: null, resetLayoutButton: null,
    labelModeInputs: [], searchModeInputs: [],
  }, canvas, details);
  const neighbors = Array.from({ length: 12 }, (_, index) => `neighbor-${index}`);
  const ids = ["hub", ...neighbors, ...Array.from({ length: 13 }, (_, index) => `other-${index}`)];
  const graph = normalizeGraphPayload({
    nodes: ids.map((id) => ({ data: { id, title: id } })),
    edges: neighbors.map((id, index) => ({ data: {
      id: `edge-${index}`,
      source: index % 2 === 0 ? "hub" : id,
      target: index % 2 === 0 ? id : "hub",
    } })),
  });
  // Default positions overlap: an explicit reveal must survive the collision
  // cull as well as the ten-neighbor cap on automatic labels.
  const cy = cytoscape({ headless: true, elements: toCytoscapeElements(graph) });
  instances.push(cy);
  runtime.state.cy = cy;
  runtime.state.currentGraphElements = graph;
  runtime.state.displayGraphElements = graph;
  bindGraphEvents(runtime, cy);
  const tap = (id: string) => cy.getElementById(id).emit("tap");
  const labeled = () => cy.nodes(".is-labeled").map((node) => node.id()).sort();
  return { runtime, cy, neighbors, tap, labeled };
};

describe("selected node neighbor labels", () => {
  it("reveals inbound and outbound neighbor labels on a second tap and restores auto on a third", () => {
    const { cy, neighbors, tap, labeled } = mountGraph();
    tap("hub");
    expect(labeled()).toEqual(["hub"]);
    tap("hub");
    expect(labeled()).toEqual(["hub", ...neighbors].sort());
    expect(cy.nodes(":selected").map((node) => node.id())).toEqual(["hub"]);
    expect(cy.getElementById("other-0").hasClass("is-dimmed")).toBe(true);
    // Selection synchronization and viewport changes must not toggle it again.
    cy.getElementById("hub").emit("select");
    cy.emit("viewport");
    expect(labeled()).toEqual(["hub", ...neighbors].sort());
    tap("hub");
    expect(labeled()).toEqual(["hub"]);
    expect(cy.getElementById("hub").selected()).toBe(true);
  });

  it("resets when another node is selected or the canvas clears selection", () => {
    const { runtime, cy, tap, labeled } = mountGraph();
    tap("hub");
    tap("hub");
    tap("other-0");
    expect(runtime.state.showSelectedNeighborLabels).toBe(false);
    tap("hub");
    expect(labeled()).toEqual(["hub"]);
    tap("hub");
    cy.emit("tap");
    expect(runtime.state.selectedNodeID).toBe("");
    expect(runtime.state.showSelectedNeighborLabels).toBe(false);
    tap("hub");
    expect(labeled()).toEqual(["hub"]);
  });

  it("resets if a rebuild removes the selected node", () => {
    const { runtime, tap } = mountGraph();
    tap("hub");
    tap("hub");
    runtime.state.displayGraphElements = emptyGraphElements();
    applyGraphElements(runtime, emptyGraphElements(), {});
    expect(runtime.state.selectedNodeID).toBe("");
    expect(runtime.state.showSelectedNeighborLabels).toBe(false);
  });

  it("supports the same toggle through keyboard activation", () => {
    const { runtime, neighbors, tap, labeled } = mountGraph();
    tap("hub");
    handleGraphKeyDown(runtime, new KeyboardEvent("keydown", { key: "Enter" }));
    expect(labeled()).toEqual(["hub", ...neighbors].sort());
    handleGraphKeyDown(runtime, new KeyboardEvent("keydown", { key: " " }));
    expect(labeled()).toEqual(["hub"]);
  });
});
