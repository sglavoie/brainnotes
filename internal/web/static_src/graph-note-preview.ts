import type { GraphNodeModel } from "./graph";
import { readNoteReference } from "./note-reference";
import { readableExcerpt } from "./note-excerpt";
import { connectionButton } from "./connection-explorer";
import { normalizeRelatedResponse } from "./related-api";

const requests = new WeakMap<HTMLElement, { key: string; abort: AbortController }>();

export const updateGraphNotePreview = (panel: HTMLElement | null, model: GraphNodeModel | null): void => {
  if (!panel) return;
  const excerpt = panel.querySelector<HTMLElement>("[data-graph-note-excerpt]");
  const connection = panel.querySelector<HTMLElement>("[data-graph-note-connection]");
  if (!excerpt || !connection) return;
  const key = selectedNoteKey(model);
  if (requests.get(panel)?.key === key) return;
  requests.get(panel)?.abort.abort();
  const abort = new AbortController(); requests.set(panel, { key, abort });
  connection.replaceChildren();
  excerpt.textContent = key ? "Loading this idea…" : "Select a note to read a passage and explore a connection.";
  if (!key) return;
  loadExcerpt(excerpt, key, abort.signal);
  void loadConnection(connection, key, abort.signal);
};

const loadExcerpt = (excerpt: HTMLElement, key: string, signal: AbortSignal): void => {
  void readNoteReference(key, signal).then((note) => {
    if (!signal.aborted) excerpt.textContent = readableExcerpt(note.excerpt || "This note has no prose yet.");
  }).catch(() => {
    if (!signal.aborted) excerpt.textContent = "Preview unavailable. You can still open this note.";
  });
};

const loadConnection = async (root: HTMLElement, source: string, signal: AbortSignal): Promise<void> => {
  try {
    const response = await fetch(`/api/related?${new URLSearchParams({ source, mode: "balanced", limit: "1" })}`, { signal });
    if (!response.ok) return;
    const related = normalizeRelatedResponse(await response.json());
    const result = related?.results[0];
    if (signal.aborted || !result) return;
    const label = document.createElement("p");
    label.textContent = `Explore alongside ${result.candidate.title}.`;
    root.replaceChildren(label, connectionButton({ source, candidate: result.candidate.key, bridge: result.bridge?.key }));
  } catch { /* A selected note remains readable if related notes are unavailable. */ }
};

const selectedNoteKey = (model: GraphNodeModel | null): string => model && !model.isAggregate ? model.id : "";
