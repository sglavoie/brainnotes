import { type GraphRuntime, findSourceNodeModel } from "./graph-controller-runtime";
import { type StatusKind, setStatusText } from "./status";

/**
 * The explicit account of what the display pipeline removed.
 *
 * Silent thinning is the failure mode this exists to prevent: whenever fewer
 * nodes are drawn than were fetched, the status line says so and names the
 * knob responsible.
 */
export const displayReductionSentence = (runtime: GraphRuntime): string => {
  const stats = runtime.displayStats;
  if (!stats || stats.displayedNodeCount >= stats.sourceNodeCount) {
    return "";
  }
  const counts = `Showing ${stats.displayedNodeCount} of ${stats.sourceNodeCount} nodes`;
  const reasons: string[] = [];
  if (stats.isolated) {
    const anchor = findSourceNodeModel(runtime, runtime.state.display.isolateID);
    const anchorLabel = anchor ? anchor.title || anchor.id : runtime.state.display.isolateID;
    // The radius is fixed, but the sentence still names it: "isolated from X"
    // would leave the user guessing how much of the neighborhood is on screen.
    reasons.push(`isolated 1 hop from "${anchorLabel}"`);
  }
  if (stats.droppedByDegree > 0) {
    reasons.push(`degree ≥ ${runtime.state.display.minDegree}`);
  }
  if (stats.aggregateCount > 0) {
    reasons.push(`folders collapsed to depth ${runtime.state.display.collapseDepth}`);
  }
  return reasons.length === 0 ? `${counts}.` : `${counts} — ${reasons.join(", ")}.`;
};

export const renderStatus = (runtime: GraphRuntime): void => {
  const message = [runtime.baseStatusMessage, displayReductionSentence(runtime), runtime.displayNotice]
    .filter((part) => part !== "")
    .join(" ");
  setStatusText(runtime.dom.status, message, runtime.baseStatusKind, [
    "is-loading",
    "is-empty",
    "is-error",
  ]);
};

export const setStatus = (runtime: GraphRuntime, message: string, kind: StatusKind = ""): void => {
  runtime.baseStatusMessage = message;
  runtime.baseStatusKind = kind;
  if (kind !== "is-loading") {
    runtime.settledStatusMessage = message;
    runtime.settledStatusKind = kind;
  }
  renderStatus(runtime);
};
