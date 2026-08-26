import type { SavedViewSummary } from "./api-types";
import {
  savedViewDiffText,
  savedViewHref,
  savedViewTitle,
  type SavedViewPendingSave,
} from "./saved-views-model";

/**
 * Fills the nav strip.
 *
 * An enabled view is an anchor, so it is shareable, middle-clickable and
 * keyboard reachable like every other nav entry. A disabled view is a plain
 * span carrying its parse error: it is shown, never dropped and never
 * followable, which is the session 11 contract rendered.
 */
export const renderSavedViewsNav = (
  container: HTMLElement,
  views: SavedViewSummary[],
  basePath: string
): number => {
  const doc = container.ownerDocument;
  container.textContent = "";
  for (const view of views) {
    if (view.enabled) {
      const link = doc.createElement("a");
      link.className = "saved-views-nav-item";
      link.href = savedViewHref(view, basePath);
      link.title = savedViewTitle(view);
      link.textContent = view.name;
      link.setAttribute("data-saved-view", view.name);
      container.appendChild(link);
      continue;
    }
    const item = doc.createElement("span");
    item.className = "saved-views-nav-item is-disabled";
    item.title = savedViewTitle(view);
    item.textContent = view.name;
    item.setAttribute("data-saved-view", view.name);
    item.setAttribute("data-saved-view-detail", view.detail);
    item.setAttribute("aria-disabled", "true");
    container.appendChild(item);
  }
  return views.length;
};

/**
 * Shows the brainnotes.toml diff an overwrite is about to write, and hides the
 * block again for a create — which never asks for confirmation and so has
 * nothing to show.
 */
export const renderSavedViewDiff = (
  block: HTMLElement | null,
  pending: SavedViewPendingSave | null
): void => {
  if (!block) {
    return;
  }
  if (!pending || pending.diff.length === 0) {
    block.textContent = "";
    block.hidden = true;
    return;
  }
  block.textContent = savedViewDiffText(pending.diff);
  block.hidden = false;
};
