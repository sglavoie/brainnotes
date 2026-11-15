import type { GraphNodeModel } from "./graph";

// Describe the mounted view, including collapsed folders. With more folders
// than colours two swatches share one, so every folder name is kept.
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
  // Say that colours repeat only when they do: with fewer folders than colours
  // every folder has its own, and the caveat would be a false alarm.
  const label = document.getElementById("graph-legend-label");
  if (label) label.textContent = new Set(folders.values()).size < folders.size ? "Folder colours · Colours may repeat" : "Folder colours";
};
