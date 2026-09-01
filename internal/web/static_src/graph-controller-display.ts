import { applyGraphElements } from "./graph-controller-mount";
import { syncPageDisplayParams } from "./graph-controller-params";
import {
  type GraphRuntime,
  computeDisplayedMatchIDs,
  currentDisplayOptions,
  elementSignature,
} from "./graph-controller-runtime";
import { renderStatus } from "./graph-controller-status";
import { type GraphLayoutRunOptions } from "./graph-controller-types";
import {
  type GraphDisplayStats,
  buildDisplayGraph,
} from "./graph-controller-view";
import {
  stopActiveLayout,
  updateDisplayControls,
  updateZoomMatchesButton,
} from "./graph-controller-visual";
import { type GraphElements } from "./graph";

/**
 * Runs the display pipeline over the fetched graph and mounts the result.
 *
 * The one place display state becomes pixels: every knob goes through here, and
 * nothing here refetches.
 */
export const rebuildDisplay = (
  runtime: GraphRuntime,
  layoutOptions: GraphLayoutRunOptions,
  prebuilt: { elements: GraphElements; stats: GraphDisplayStats } | null = null
): void => {
  const { state } = runtime;
  const built =
    prebuilt || buildDisplayGraph(state.currentGraphElements, currentDisplayOptions(runtime));

  // A query change can filter the anchor out from under a live isolation. The
  // pipeline treats that as a no-op, so without this the graph would silently
  // spring back to full while the URL and the clear button still claimed an
  // isolated view.
  if (state.display.isolateID !== "" && !built.stats.isolated) {
    const droppedAnchor = state.display.isolateID;
    state.display.isolateID = "";
    syncPageDisplayParams(state.display);
    runtime.displayNotice = `Isolation cleared: "${droppedAnchor}" is not in the current graph.`;
  } else {
    runtime.displayNotice = "";
  }

  state.displayGraphElements = built.elements;
  runtime.displayStats = built.stats;
  runtime.displayedMatchIDs = computeDisplayedMatchIDs(runtime);
  runtime.currentElementSignature = elementSignature(built.elements);
  applyGraphElements(runtime, built.elements, layoutOptions);
  updateZoomMatchesButton(runtime);
  updateDisplayControls(runtime);
  renderStatus(runtime);
};

/** A display-knob rebuild: relayout in place, never yanking the viewport. */
export const rebuildDisplayForControl = (runtime: GraphRuntime): void => {
  rebuildDisplay(runtime, {
    fitAfterLayout: false,
    preserveViewport: true,
    resetPositions: false,
  });
};

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
export const scheduleDisplayRebuild = (runtime: GraphRuntime, delay: number): void => {
  window.clearTimeout(runtime.displayToken);
  if (delay <= 0) {
    rebuildDisplayForControl(runtime);
    return;
  }
  stopActiveLayout(runtime);
  runtime.displayToken = window.setTimeout(() => {
    rebuildDisplayForControl(runtime);
  }, delay);
};

export const toggleFolderExpansion = (runtime: GraphRuntime, folder: string): void => {
  if (folder === "") {
    return;
  }
  if (runtime.state.expandedFolders.has(folder)) {
    runtime.state.expandedFolders.delete(folder);
  } else {
    runtime.state.expandedFolders.add(folder);
  }
  rebuildDisplayForControl(runtime);
};
