import {
  buildNotePinRemoveFetchRequest,
  buildNotePinReorderFetchRequest,
  normalizeManagementErrorMessage,
  readNotePinResponsePayload,
  type ManagementResult,
  type NotePinFetchRequest,
} from "./api";
import { narrowElement, queryAllElements, queryElement } from "./dom";
import { setStatusText } from "./status";

type PinnedListStatusKind = "" | "is-success" | "is-error";
type FetchLike = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;

export type PinnedListDeps = {
  fetch?: FetchLike;
  reload?: () => void;
};

type PendingPinnedMove = {
  key: string;
  before: string;
  after: string;
};

const draggingClass = "is-dragging";
const dropTargetClass = "is-drop-target";

export const pinnedItemKey = (item: Element | null): string =>
  narrowElement(item, HTMLElement)?.getAttribute("data-pinned-key") || "";

export const pinnedNeighborKeys = (
  item: HTMLElement
): { before: string; after: string } => ({
  before: pinnedItemKey(item.previousElementSibling),
  after: pinnedItemKey(item.nextElementSibling),
});

class PinnedList {
  private readonly csrfToken: string;
  private readonly fetcher: FetchLike;
  private readonly reload: () => void;
  private cancelDrag: ((announce?: boolean) => void) | null = null;

  constructor(root: HTMLElement, private readonly list: HTMLElement,
    private readonly statusEl: HTMLElement, deps: PinnedListDeps) {
    this.csrfToken = root.getAttribute("data-pinned-csrf") || "";
    this.fetcher = deps.fetch || fetch;
    this.reload = deps.reload || ((): void => window.location.reload());
  }

  private setStatus = (message: string, kind: PinnedListStatusKind = ""): void => {
    setStatusText(this.statusEl, message || " ", kind, ["is-success", "is-error"]);
  };

  private rows = (): HTMLElement[] => queryAllElements(this.list, "[data-pinned-item]", HTMLElement);

  private post = async (request: NotePinFetchRequest, fallback: string): Promise<boolean> => {
    try {
      const resp = await this.fetcher(request.url, request.init);
      const payload = await readNotePinResponsePayload(resp);
      if (!resp.ok) {
        this.setStatus(normalizeManagementErrorMessage(resp.status, payload), "is-error");
        return false;
      }
      const result = payload as ManagementResult;
      this.setStatus(result && result.ok && result.message ? result.message : fallback, "is-success");
      return true;
    } catch {
      this.setStatus("Pin request failed.", "is-error");
      return false;
    }
  };

  private moveQueue: Promise<void> = Promise.resolve();
  private queuedMoves = 0;
  private resyncing = false;

  private sendMove = async (move: PendingPinnedMove): Promise<boolean> => {
    if (!move.key || !this.csrfToken) {
      this.setStatus("Reorder unavailable.", "is-error");
      return false;
    }
    this.setStatus("Saving pin order...");
    return this.post(
      buildNotePinReorderFetchRequest({
        key: move.key,
        token: this.csrfToken,
        before: move.before,
        after: move.after,
      }),
      "Reordered pins."
    );
  };

  // Moves run one at a time so the server applies them in the order the user
  // made them, which is also what keeps each request's neighbours valid: they
  // were read against the list state the previous request leaves on disk.
  // restore is the sibling the row sat in front of before it moved, and a
  // refused move may only use it while it is the last move queued — a later
  // move has already rewritten the list, so undoing an older one would
  // scramble it, and the server-rendered page becomes the only truth left.
  // The rank badges are not updated: the request names neighbours rather than
  // ranks, so a stale badge costs nothing until the next page load.
  private enqueueMove = (item: HTMLElement, restore: Element | null): void => {
    this.queuedMoves += 1;
    const sequence = this.queuedMoves;
    const neighbors = pinnedNeighborKeys(item);
    const move: PendingPinnedMove = {
      key: pinnedItemKey(item),
      before: neighbors.before,
      after: neighbors.after,
    };
    this.setStatus("Saving pin order...");
    this.moveQueue = this.moveQueue.then(async () => {
      if (this.resyncing) {
        return;
      }
      if (await this.sendMove(move)) {
        return;
      }
      this.cancelDrag?.(false);
      if (sequence === this.queuedMoves) {
        this.list.insertBefore(item, restore);
        return;
      }
      this.resyncing = true;
      this.reload();
    });
  };

  private moveByKeyboard = (item: HTMLElement, direction: -1 | 1): void => {
    this.cancelDrag?.();
    const restore = item.nextElementSibling;
    if (direction < 0) {
      const previous = item.previousElementSibling;
      if (!previous) {
        return;
      }
      this.list.insertBefore(item, previous);
    } else {
      const next = item.nextElementSibling;
      if (!next) {
        return;
      }
      this.list.insertBefore(item, next.nextElementSibling);
    }
    this.enqueueMove(item, restore);
  };

  private startDrag = (item: HTMLElement, event: PointerEvent): void => {
    if (event.button !== 0) {
      return;
    }
    event.preventDefault();
    this.cancelDrag?.();
    const restore = item.nextElementSibling;
    item.classList.add(draggingClass);

    const clearDropTarget = (): void => {
      for (const row of this.rows()) {
        row.classList.remove(dropTargetClass);
      }
    };

    const onPointerMove = (moveEvent: PointerEvent): void => {
      if (moveEvent.pointerId !== event.pointerId) return;
      const next = this.rows().find((row) => {
        if (row === item) {
          return false;
        }
        const rect = row.getBoundingClientRect();
        return moveEvent.clientY < rect.top + rect.height / 2;
      });
      clearDropTarget();
      if (next) {
        next.classList.add(dropTargetClass);
        this.list.insertBefore(item, next);
      } else {
        this.list.appendChild(item);
      }
    };

    const finishDrag = (cancelled: boolean, announce = true): void => {
      item.classList.remove(draggingClass);
      clearDropTarget();
      window.removeEventListener("pointermove", onPointerMove);
      window.removeEventListener("pointerup", releaseDrag);
      window.removeEventListener("pointercancel", onPointerCancel);
      window.removeEventListener("keydown", onKeyDown);
      this.cancelDrag = null;
      if (cancelled) {
        this.list.insertBefore(item, restore);
        if (announce) this.setStatus("Pin reorder cancelled.");
        return;
      }
      if (item.nextElementSibling === restore) {
        return;
      }
      this.enqueueMove(item, restore);
    };

    const releaseDrag = (upEvent: PointerEvent): void => {
      if (upEvent.pointerId === event.pointerId) finishDrag(false);
    };
    const cancelDrag = (announce = true): void => finishDrag(true, announce);
    const onPointerCancel = (cancelEvent: PointerEvent): void => {
      if (cancelEvent.pointerId === event.pointerId) cancelDrag();
    };
    const onKeyDown = (keyEvent: KeyboardEvent): void => {
      if (keyEvent.key !== "Escape") return;
      keyEvent.preventDefault();
      cancelDrag();
    };
    this.cancelDrag = cancelDrag;
    window.addEventListener("keydown", onKeyDown);
    window.addEventListener("pointermove", onPointerMove);
    window.addEventListener("pointerup", releaseDrag);
    window.addEventListener("pointercancel", onPointerCancel);
  };

  // A row that leaves the list changes nothing about the remaining ranks, but
  // the page is server-rendered, so a reload is what restores the empty state
  // and the numbering.
  private unpin = async (item: HTMLElement, button: HTMLButtonElement): Promise<void> => {
    this.cancelDrag?.();
    const key = pinnedItemKey(item);
    if (!key || !this.csrfToken) {
      this.setStatus("Unpin unavailable.", "is-error");
      return;
    }
    button.disabled = true;
    this.setStatus("Unpinning note...");
    const ok = await this.post(
      buildNotePinRemoveFetchRequest({ key, token: this.csrfToken }),
      "Unpinned note."
    );
    button.disabled = false;
    if (ok) {
      this.reload();
    }
  };

  bind(): void {
    for (const item of this.rows()) {
      const handle = queryElement(item, "[data-pinned-handle]", HTMLButtonElement);
      if (handle) {
        handle.addEventListener("pointerdown", (event) => this.startDrag(item, event));
        handle.addEventListener("keydown", (event) => {
          if (event.key !== "ArrowUp" && event.key !== "ArrowDown") {
            return;
          }
          event.preventDefault();
          this.moveByKeyboard(item, event.key === "ArrowUp" ? -1 : 1);
        });
      }

      const pinButton = queryElement(item, "[data-pin-button]", HTMLButtonElement);
      if (pinButton) {
        pinButton.addEventListener("click", () => {
          void this.unpin(item, pinButton);
        });
      }
    }
  }
}

export const initPinnedList = (deps: PinnedListDeps = {}): void => {
  const root = queryElement(document, "[data-pinned-root]", HTMLElement);
  if (!root) return;
  const list = queryElement(root, "[data-pinned-list]", HTMLElement);
  const statusEl = queryElement(root, "[data-pinned-status]", HTMLElement);
  if (!list || !statusEl) return;
  new PinnedList(root, list, statusEl, deps).bind();
};
