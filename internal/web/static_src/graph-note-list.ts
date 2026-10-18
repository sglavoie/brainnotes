import type { GraphRuntime } from "./graph-controller-runtime";
import type { GraphNodeModel } from "./graph";

const ordered = new WeakMap<GraphNodeModel[], GraphNodeModel[]>();
const rendered = new WeakMap<HTMLSelectElement, GraphNodeModel[]>();
export const orderedGraphNodes = (runtime: GraphRuntime): GraphNodeModel[] => {
  const nodes = runtime.state.displayGraphElements.nodes;
  let result = ordered.get(nodes);
  if (!result) {
    result = [...nodes].sort((a, b) => a.title.localeCompare(b.title) || a.id.localeCompare(b.id));
    ordered.set(nodes, result);
  }
  return result;
};

// Reuse option nodes until the displayed graph changes: moving the pointer or
// choosing a note must not reset the native listbox's keyboard position.
export const renderGraphNoteList = (runtime: GraphRuntime): void => {
  const list = document.getElementById("graph-node-list") as HTMLSelectElement | null;
  if (!list) return;
  const nodes = orderedGraphNodes(runtime);
  if (rendered.get(list) !== nodes) {
    const options = nodes.map((node) => new Option(
      node.isAggregate ? `Expand folder: ${node.folder}` : `${node.title}${node.folder ? ` — ${node.folder}` : ""}`, node.id
    ));
    list.replaceChildren(...options);
    rendered.set(list, nodes);
  }
  list.disabled = nodes.length === 0;
  list.value = runtime.state.selectedNodeID;
};

export const bindGraphNoteList = (runtime: GraphRuntime, select: (id: string) => void): void => {
  const list = document.getElementById("graph-node-list") as HTMLSelectElement | null;
  list?.addEventListener("change", () => {
    const node = runtime.state.displayGraphElements.nodes.find((node) => node.id === list.value);
    if (!node) return;
    if (node.isAggregate) runtime.toggleFolderExpansion(node.folder);
    else select(node.id);
  });
};
