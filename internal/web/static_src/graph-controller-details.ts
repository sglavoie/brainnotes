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

export const issueItemsForNode = (data: GraphNodeModel | null | undefined): GraphIssueItem[] => {
  if (!data) {
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

export const setOpenNoteTarget = (openNote: HTMLAnchorElement | null, nodeID: string): void => {
  if (!openNote) {
    return;
  }

  const path = notePathForKey(nodeID);
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

  if (refs.hoverTitle) {
    refs.hoverTitle.textContent = model ? model.title || model.id : "Hover a node";
  }
  if (refs.hoverFolder) {
    refs.hoverFolder.textContent = model ? `Folder: ${model.folder || "/"}` : "";
  }
  if (refs.hoverDegree) {
    refs.hoverDegree.textContent = model ? formatDegreeDetails(model) : "";
  }
  renderIssueDetails(refs.hoverIssues, model);
  if (refs.detailsPanel) {
    refs.detailsPanel.classList.toggle("is-locked", Boolean(selectedModel));
    refs.detailsPanel.classList.toggle("is-transient", !selectedModel && Boolean(hoveredModel));
  }
  setOpenNoteTarget(refs.openNote, selectedModel ? selectedModel.id : "");
};
