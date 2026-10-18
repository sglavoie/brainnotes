import { notePathForKey } from "./filters";
import { type GraphNodeModel, readDegreeValue } from "./graph";

export type GraphIssueItem = {
  className: string;
  label: string;
};

export type GraphDetailsRefs = {
  detailsPanel: HTMLElement | null;
  hoverTitle: HTMLElement | null;
  hoverFolder: HTMLElement | null;
  hoverDegree: HTMLElement | null;
  hoverIssues: HTMLElement | null;
  openNote: HTMLAnchorElement | null;
};

export const formatDegreeDetails = (data: GraphNodeModel | null | undefined): string => {
  if (!data) {
    return "";
  }
  const inDegree = readDegreeValue(data.inDegree, 0);
  const outDegree = readDegreeValue(data.outDegree, 0);
  const degree = readDegreeValue(data.degree, inDegree + outDegree);
  const filteredInDegree = readDegreeValue(data.filteredInDegree, inDegree);
  const filteredOutDegree = readDegreeValue(data.filteredOutDegree, outDegree);
  const filteredDegree = readDegreeValue(data.filteredDegree, filteredInDegree + filteredOutDegree);
  const visibleDetail = `Visible degree: ${filteredDegree} (${filteredInDegree} in, ${filteredOutDegree} out)`;
  if (filteredDegree === degree && filteredInDegree === inDegree && filteredOutDegree === outDegree) {
    return visibleDetail;
  }
  return `${visibleDetail}. Full graph: ${degree} (${inDegree} in, ${outDegree} out)`;
};

/**
 * The degree line for a collapsed folder aggregate: it stands for a group of
 * notes, so member count is the only honest number to show. Its degree counts
 * describe synthesized inter-folder edges, which would read as a note's own
 * link count and mean something else entirely.
 */
export const formatAggregateDetails = (data: GraphNodeModel | null | undefined): string => {
  const memberCount = data && data.memberIDs ? data.memberIDs.length : 0;
  return memberCount === 1 ? "Collapsed folder: 1 note" : `Collapsed folder: ${memberCount} notes`;
};

export const issueItemsForNode = (data: GraphNodeModel | null | undefined): GraphIssueItem[] => {
  if (!data || data.isAggregate) {
    // An aggregate carries no issue flags of its own; its members' flags are not
    // summable into one badge, so the list stays empty rather than lying.
    return [];
  }
  const items: GraphIssueItem[] = [];
  const brokenCount = readDegreeValue(data.brokenCount, 0);
  if (data.hasBroken || brokenCount > 0) {
    items.push({
      className: "is-broken",
      label:
        brokenCount > 0
          ? brokenCount === 1
            ? "1 broken link"
            : `${brokenCount} broken links`
          : "Broken links",
    });
  }
  if (data.isOrphan) {
    items.push({
      className: "is-orphan",
      label: "Orphan",
    });
  }
  if (data.isDeadEnd) {
    items.push({
      className: "is-dead-end",
      label: "Dead end",
    });
  }
  return items;
};

export const renderIssueDetails = (
  hoverIssues: HTMLElement | null,
  model: GraphNodeModel | null | undefined
): void => {
  if (!hoverIssues) {
    return;
  }
  hoverIssues.textContent = "";
  const items = issueItemsForNode(model);
  hoverIssues.hidden = items.length === 0;
  for (const item of items) {
    const issue = document.createElement("li");
    issue.className = `graph-issue-item ${item.className}`;
    issue.textContent = item.label;
    hoverIssues.appendChild(issue);
  }
};

export const setOpenNoteTarget = (
  openNote: HTMLAnchorElement | null,
  nodeID: string,
  isAggregate = false
): void => {
  if (!openNote) {
    return;
  }

  // An aggregate is a synthetic folder node with no note behind it. The branch is
  // driven by the explicit flag rather than by `notePathForKey` failing, because a
  // real note key may legitimately start with the aggregate id prefix.
  const path = isAggregate ? "" : notePathForKey(nodeID);
  if (!path) {
    openNote.removeAttribute("href");
    openNote.classList.add("is-disabled");
    openNote.setAttribute("aria-disabled", "true");
    openNote.setAttribute("tabindex", "-1");
    return;
  }

  openNote.setAttribute("href", path);
  openNote.classList.remove("is-disabled");
  openNote.setAttribute("aria-disabled", "false");
  openNote.setAttribute("tabindex", "0");
};

// Renders the hover/details panel from already-resolved models: `selectedModel`
// locks the panel, `hoveredModel` is the transient fallback. Callers own the
// state lookups so this stays a pure view over the passed-in DOM refs.
export const renderGraphDetails = (
  refs: GraphDetailsRefs,
  selectedModel: GraphNodeModel | null,
  hoveredModel: GraphNodeModel | null
): void => {
  const model = selectedModel || hoveredModel;

  renderDetailText(refs, model);
  renderIssueDetails(refs.hoverIssues, model);
  if (refs.detailsPanel) {
    refs.detailsPanel.classList.toggle("is-locked", Boolean(selectedModel));
    refs.detailsPanel.classList.toggle("is-transient", !selectedModel && Boolean(hoveredModel));
    refs.detailsPanel.classList.toggle("is-aggregate", Boolean(model && model.isAggregate));
  }
  setOpenNoteTarget(
    refs.openNote,
    selectedModel ? selectedModel.id : "",
    Boolean(selectedModel && selectedModel.isAggregate)
  );
};

const renderDetailText = (refs: GraphDetailsRefs, model: GraphNodeModel | null): void => {
  if (refs.hoverTitle) {
    refs.hoverTitle.textContent = model ? model.title || model.id : "Choose a note";
  }
  if (refs.hoverFolder) {
    refs.hoverFolder.textContent = model ? `Folder: ${model.folder || "/"}` : "";
  }
  if (refs.hoverDegree) {
    if (!model) {
      refs.hoverDegree.textContent = "";
    } else {
      refs.hoverDegree.textContent = model.isAggregate
        ? formatAggregateDetails(model)
        : formatDegreeDetails(model);
    }
  }
};
