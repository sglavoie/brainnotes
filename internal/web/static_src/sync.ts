import { queryElement } from "./dom";
import { createSourceSurfaceFactory } from "./source-editor-view";
import { formatSource } from "./source-format";
import { createSyncConflictsPanel } from "./sync-conflicts-panel";
import { createSyncStatusPanel } from "./sync-status-panel";
import type { SourceSurface, SourceSurfaceFactory } from "./source-surface";

// The /sync page's entry point: the status panel, the conflict review, and the one
// wire between them.
//
// That wire is the page's whole claim to honesty. A resolution does not tell the
// status panel what changed; it tells it to read again. Whether the peer has
// acknowledged the change, whether the export has caught up, whether another
// package is now unblocked are all facts on this disk, and the panel that reports
// them asks the server rather than adjusting a number it was holding.
//
// A page without the panels returns immediately, so the bundle stays one file.

export type SyncPageDeps = {
  doc?: Document;
  view?: Window | null;
};

// mergeSurface builds one text surface for one merge.
//
// It is built per merge and closes over the surface it built, because a formatting
// keystroke has to edit the document it was typed into. Ctrl/⌘ + S is deliberately
// inert: there is nothing on this page a keystroke could save — a merge becomes a
// change to the vault only through Apply, after the plan has been read — and the
// text is kept in this browser on its own as it is typed.
const mergeSurface = (): SourceSurfaceFactory => {
  let surface: SourceSurface | null = null;
  const build = createSourceSurfaceFactory({
    onSave: () => {},
    onCommand: (command, options) => {
      if (!surface) {
        return;
      }
      surface.applyEdit(formatSource(surface.getValue(), surface.getSelection(), command, options));
      surface.focus();
    },
  });
  return (host: HTMLElement, initialValue: string): SourceSurface => {
    surface = build(host, initialValue);
    return surface;
  };
};

export const initSync = (deps: SyncPageDeps = {}): void => {
  const doc = deps.doc ?? document;
  const view = deps.view ?? window;
  const panelRoot = queryElement(doc, "[data-sync-panel]", HTMLElement);
  const conflictsRoot = queryElement(doc, "[data-sync-conflicts]", HTMLElement);
  if (!panelRoot && !conflictsRoot) {
    return;
  }
  const fetcher = (input: string, init?: RequestInit): Promise<Response> => view.fetch(input, init);
  const status = panelRoot ? createSyncStatusPanel({ root: panelRoot, fetch: fetcher }) : null;
  const conflicts = conflictsRoot
    ? createSyncConflictsPanel({
        root: conflictsRoot,
        fetch: fetcher,
        surface: mergeSurface,
        changed: () => void status?.load(),
        view,
      })
    : null;
  void status?.load();
  void conflicts?.load();
};
