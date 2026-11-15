import type { GraphNodeModel } from "./graph";

// Describe the mounted view, including collapsed folders. Folder hashes can
// collide, so keep every folder name even when two swatches share a colour.
export const renderGraphLegend = (nodes: readonly GraphNodeModel[]): void => {
  const legend = document.getElementById("graph-folder-legend");
  if (!legend) return;
  const folders = new Map(nodes.map((node) => [node.folder, node.color]));
  const items = [...folders].sort(([a], [b]) => a.localeCompare(b)).map(([folder, color]) => {
    const item = document.createElement("li");
    const swatch = document.createElement("span");
    swatch.textContent = "● ";
    swatch.style.color = color;
    swatch.setAttribute("aria-hidden", "true");
    item.append(swatch, folder || "Vault root");
    return item;
  });
  legend.replaceChildren(...items);
};
