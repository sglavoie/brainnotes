import type { GraphViewportSnapshot } from "./graph";

export const readSpread = (input: HTMLInputElement | null): number => {
  if (!input) {
    return 2;
  }
  const parsed = Number.parseFloat(input.value);
  if (!Number.isFinite(parsed) || parsed <= 0) {
    return 2;
  }
  return parsed;
};

export const clampedSpread = (input: HTMLInputElement | null): number =>
  Math.min(6, Math.max(0.8, readSpread(input)));

export const updateSpreadLabel = (
  input: HTMLInputElement | null,
  spreadValue: HTMLElement | null
): void => {
  if (!spreadValue) {
    return;
  }
  const spread = readSpread(input);
  spreadValue.textContent = `${spread.toFixed(1)}x`;
};

/**
 * Whether `cytoscape.use(cytoscapeFcose)` has already run in this page. fcose
 * registration is global to the Cytoscape factory, so this is module state
 * rather than controller state.
 */
let fcoseRegistered = false;

/**
 * Registers the vendored fcose layout against the global Cytoscape factory.
 *
 * Deliberately defensive: the extension script tag can be missing (Session 7's
 * QA pass removes it on purpose to confirm the cose fallback), and the jsdom
 * test fake is a bare factory function with no `.use` property. Either case
 * must no-op silently, leaving `hasFcose()` false, rather than throw.
 *
 * Idempotent — repeated calls after a successful registration do nothing.
 */
export const registerGraphLayouts = (): void => {
  if (fcoseRegistered) {
    return;
  }
  const extension = window.cytoscapeFcose;
  if (!extension) {
    return;
  }
  const factory = window.cytoscape as unknown as { use?: unknown } | undefined;
  if (!factory || typeof factory.use !== "function") {
    return;
  }
  (factory.use as (ext: unknown) => void)(extension);
  fcoseRegistered = true;
};

/** Reports whether fcose registration succeeded, i.e. whether `name: "fcose"` is usable. */
export const hasFcose = (): boolean => fcoseRegistered;

/**
 * The fallback layout, used whenever fcose is unavailable.
 *
 * `nodeDimensionsIncludeLabels` is set here too: cose supports it and merely
 * defaults it off, so leaving it unset was what let the fallback pack circles
 * and collide labels afterwards.
 */
export const coseLayoutOptionsForSpread = (spread: number): cytoscape.CoseLayoutOptions => ({
  name: "cose",
  animate: false,
  fit: false,
  randomize: false,
  padding: 72,
  nodeDimensionsIncludeLabels: true,
  idealEdgeLength: Math.round(70 + 55 * spread),
  nodeRepulsion: Math.round(7000 * spread * spread),
  componentSpacing: Math.round(100 + 85 * spread),
  nodeOverlap: Math.round(12 + 16 * spread),
  gravity: Math.max(0.05, 0.62 - 0.09 * spread),
  numIter: Math.round(900 + 120 * spread),
  initialTemp: Math.round(180 + 45 * spread),
  coolingFactor: 0.95,
  minTemp: 1,
});

/**
 * The fcose options, spread-scaled from the same formulas the cose fallback
 * uses so the Spread slider keeps its feel across the whole 0.8-6.0 range.
 *
 * `randomize: false` leaves `seedDeterministicPositions` governing the starting
 * arrangement, which is what keeps a layout run reproducible. fcose's own
 * option names (`quality`, `packComponents`, `tile`, `nodeSeparation`) are not
 * in `@types/cytoscape`, so the result goes out through the same
 * `as unknown as` escape hatch `graph-controller-style.ts` uses for
 * `StylesheetJson`.
 */
const fcoseLayoutOptionsForSpread = (spread: number): cytoscape.LayoutOptions =>
  ({
    name: "fcose",
    quality: "proof",
    animate: false,
    fit: false,
    randomize: false,
    padding: 72,
    nodeDimensionsIncludeLabels: true,
    packComponents: true,
    tile: true,
    idealEdgeLength: Math.round(70 + 55 * spread),
    nodeRepulsion: Math.round(7000 * spread * spread),
    nodeSeparation: Math.round(75 + 45 * spread),
    gravity: Math.max(0.05, 0.62 - 0.09 * spread),
    numIter: Math.round(900 + 120 * spread),
  }) as unknown as cytoscape.LayoutOptions;

/** The layout the controller actually runs: fcose when registered, cose otherwise. */
export const layoutOptionsForSpread = (spread: number): cytoscape.LayoutOptions =>
  hasFcose()
    ? fcoseLayoutOptionsForSpread(spread)
    : (coseLayoutOptionsForSpread(spread) as unknown as cytoscape.LayoutOptions);

export const seedDeterministicPositions = (cy: cytoscape.Core | null, spread: number): void => {
  if (!cy) {
    return;
  }
  const nodes = cy
    .nodes()
    .toArray()
    .sort((left, right) => left.id().localeCompare(right.id())) as cytoscape.NodeSingular[];
  const radialGap = 52 + 26 * spread;
  const goldenAngle = Math.PI * (3 - Math.sqrt(5));

  nodes.forEach((node, index) => {
    const radius = radialGap * Math.sqrt(index);
    const angle = index * goldenAngle;
    node.position({
      x: Math.cos(angle) * radius,
      y: Math.sin(angle) * radius,
    });
  });
};

export const separateIsolatedNodes = (cy: cytoscape.Core | null, spread: number): void => {
  if (!cy) {
    return;
  }
  const isolatedNodes = cy
    .nodes()
    .filter((node) => node.connectedEdges().length === 0)
    .toArray()
    .sort((left, right) => left.id().localeCompare(right.id())) as cytoscape.NodeSingular[];
  if (isolatedNodes.length === 0) {
    return;
  }

  const spacing = 58 + 34 * spread;
  const columns = Math.ceil(Math.sqrt(isolatedNodes.length));
  const rows = Math.ceil(isolatedNodes.length / columns);
  const connectedNodes = cy.nodes().filter((node) => node.connectedEdges().length > 0);
  let startX = -((columns - 1) * spacing) / 2;
  let startY = -((rows - 1) * spacing) / 2;

  if (connectedNodes.length > 0) {
    const box = connectedNodes.boundingBox();
    startX = box.x2 + spacing * 1.6;
    startY = box.y1 + (box.h - (rows - 1) * spacing) / 2;
  }

  isolatedNodes.forEach((node, index) => {
    const column = index % columns;
    const row = Math.floor(index / columns);
    node.position({
      x: startX + column * spacing,
      y: startY + row * spacing,
    });
  });
};

export const currentViewport = (cy: cytoscape.Core | null): GraphViewportSnapshot | null => {
  if (!cy) {
    return null;
  }
  const pan = cy.pan();
  return {
    pan: {
      x: pan.x,
      y: pan.y,
    },
    zoom: cy.zoom(),
  };
};

export const applyViewport = (
  cy: cytoscape.Core | null,
  viewport: GraphViewportSnapshot | null
): void => {
  if (!cy || !viewport) {
    return;
  }
  cy.viewport({
    pan: {
      x: viewport.pan.x,
      y: viewport.pan.y,
    },
    zoom: viewport.zoom,
  });
};
