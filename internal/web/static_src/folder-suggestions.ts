import { normalizeFacetOptions } from "./api-facets";

// Suggestions never constrain the destination: new paths and the empty vault
// root remain valid, including when the optional lookup fails.
export const initFolderSuggestions = (root: ParentNode = document): void => {
  const inputs = Array.from(root.querySelectorAll<HTMLInputElement>("[data-folder-suggestions]"));
  if (!inputs.length) return;
  const doc = inputs[0].ownerDocument;
  const list = doc.createElement("datalist");
  list.id = "bn-folder-suggestions";
  doc.body.append(list);
  let loaded = false;
  let loading = false;
  const load = async (): Promise<void> => {
    if (loaded || loading) return;
    loading = true;
    try {
      const response = await fetch("/api/facets", { headers: { Accept: "application/json" } });
      if (!response.ok) return;
      const folders = normalizeFacetOptions(await response.json())
        .filter((option) => option.param === "folder")
        .sort((a, b) => a.value.localeCompare(b.value));
      list.replaceChildren(...folders.map((folder) => {
        const option = doc.createElement("option");
        option.value = folder.value;
        return option;
      }));
      loaded = true;
    } catch {
      // Keep manual entry available and retry the next time a field is focused.
    } finally {
      loading = false;
    }
  };
  for (const input of inputs) {
    input.setAttribute("list", list.id);
    input.addEventListener("focus", () => { void load(); });
  }
  // A field can already own focus before the deferred bundle finishes loading.
  if (inputs.some((input) => input === doc.activeElement)) void load();
};
