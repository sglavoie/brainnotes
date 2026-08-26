import type { PropertyConfigDiffLine, SavedViewSummary } from "./api-types";
import {
  savedViewDiffText,
  savedViewHref,
  savedViewTitle,
  type SavedViewCreateReceipt,
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
 * Fills the shared diff block, tagging *why* it is showing.
 *
 * The role attribute is the whole difference between the two callers below:
 * `confirm` is a question the confirm button answers, `receipt` is a record of
 * a write that already happened. Nothing about a receipt is clickable, so the
 * marker is what keeps styling — and a reader of the DOM — from mistaking the
 * two.
 */
const renderDiffBlock = (
  block: HTMLElement | null,
  diff: PropertyConfigDiffLine[],
  role: "confirm" | "receipt"
): void => {
  if (!block) {
    return;
  }
  if (diff.length === 0) {
    block.textContent = "";
    block.hidden = true;
    block.removeAttribute("data-saved-views-diff-role");
    return;
  }
  block.textContent = savedViewDiffText(diff);
  block.hidden = false;
  block.setAttribute("data-saved-views-diff-role", role);
};

/**
 * Shows the brainnotes.toml diff an overwrite is about to write, and hides the
 * block again once nothing is pending confirmation.
 */
export const renderSavedViewDiff = (
  block: HTMLElement | null,
  pending: SavedViewPendingSave | null
): void => {
  renderDiffBlock(block, pending ? pending.diff : [], "confirm");
};

/**
 * Shows the diff a create already wrote, as a receipt.
 *
 * This is the post-success half of the saved-views write class: a create
 * auto-chains preview into apply, so the user never sees the diff as a
 * question, and would otherwise never see it at all. It renders into the same
 * block an overwrite confirms in, but the caller keeps the confirm button
 * hidden and the receipt itself carries no preview ID, so there is nothing here
 * to act on.
 */
export const renderSavedViewReceipt = (
  block: HTMLElement | null,
  receipt: SavedViewCreateReceipt | null
): void => {
  renderDiffBlock(block, receipt ? receipt.diff : [], "receipt");
};
