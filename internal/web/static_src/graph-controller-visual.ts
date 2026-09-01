import { applyGraphVisualClasses } from "./graph-controller-labels";
import {
  applyViewport,
  clampedSpread,
  currentViewport,
  hasFcose,
  layoutOptionsForSpread,
  seedDeterministicPositions,
  separateIsolatedNodes,
} from "./graph-controller-layout";
import { formatCollapseDepthLabel, formatMinDegreeLabel } from "./graph-controller-params";
import { type GraphRuntime } from "./graph-controller-runtime";
import { buildGraphStyle } from "./graph-controller-style";
import { type GraphLayoutRunOptions } from "./graph-controller-types";

export const applyGraphVisualState = (runtime: GraphRuntime): void => {
  applyGraphVisualClasses(
    runtime.state.cy,
    runtime.state.labelMode,
    runtime.state.selectedNodeID,
    runtime.state.hoveredNodeID,
    runtime.displayedMatchIDs
  );
};

export const setLabelMode = (runtime: GraphRuntime, nextMode: string): void => {
  runtime.state.labelMode = nextMode === "all" ? "all" : "auto";
  applyGraphVisualState(runtime);
};

export const restyleGraphForTheme = (runtime: GraphRuntime): void => {
  if (!runtime.state.cy) {
    return;
  }
  // Replacing the stylesheet repaints colors only; graph data, selection,
  // viewport, and layout positions are untouched. Re-toggle visual classes so
  // label/focus state stays in sync with the freshly applied style.
  runtime.state.cy.style(buildGraphStyle());
  applyGraphVisualState(runtime);
};

export const stopActiveLayout = (runtime: GraphRuntime): void => {
  if (runtime.state.activeLayout && typeof runtime.state.activeLayout.stop === "function") {
    runtime.state.activeLayout.stop();
  }
  runtime.state.activeLayout = null;
};

export const rememberMeaningfulViewport = (runtime: GraphRuntime): void => {
  if (!runtime.state.cy || runtime.state.cy.nodes().length === 0) {
    return;
  }
  runtime.state.lastMeaningfulViewport = currentViewport(runtime.state.cy);
};

export const fitCurrentGraph = (runtime: GraphRuntime): void => {
  if (!runtime.state.cy || runtime.state.cy.nodes().length === 0) {
    return;
  }
  runtime.state.cy.fit(runtime.state.cy.elements(), 72);
  rememberMeaningfulViewport(runtime);
};

export const updateZoomMatchesButton = (runtime: GraphRuntime): void => {
  if (runtime.dom.zoomMatchesButton instanceof HTMLButtonElement) {
    runtime.dom.zoomMatchesButton.disabled = runtime.displayedMatchIDs.size === 0;
  }
};

/** Disabled-state of the display controls, refreshed on every rebuild and selection. */
export const updateDisplayControls = (runtime: GraphRuntime): void => {
  const { dom, state } = runtime;
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
};

export const zoomToMatches = (runtime: GraphRuntime): void => {
  if (!runtime.state.cy || runtime.displayedMatchIDs.size === 0) {
    return;
  }
  const matchedNodes = runtime.state.cy
    .nodes()
    .filter((node) => runtime.displayedMatchIDs.has(node.id()));
  if (matchedNodes.length === 0) {
    return;
  }
  runtime.state.cy.fit(matchedNodes, 96);
  rememberMeaningfulViewport(runtime);
};

export const runLayout = (
  runtime: GraphRuntime,
  options: GraphLayoutRunOptions = {}
): void => {
  const { dom, state } = runtime;
  if (!state.cy || state.cy.nodes().length === 0) {
    return;
  }
  const layoutOptions = options;
  const fitAfterLayout = Boolean(layoutOptions.fitAfterLayout);
  const preservedViewport = layoutOptions.preserveViewport ? currentViewport(state.cy) : null;
  const runID = state.layoutRunID + 1;
  state.layoutRunID = runID;
  stopActiveLayout(runtime);

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
      fitCurrentGraph(runtime);
    } else if (preservedViewport) {
      applyViewport(state.cy, preservedViewport);
      rememberMeaningfulViewport(runtime);
    } else {
      rememberMeaningfulViewport(runtime);
    }
    applyGraphVisualState(runtime);
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
};
