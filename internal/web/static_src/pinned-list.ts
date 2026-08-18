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

export const initPinnedList = (deps: PinnedListDeps = {}): void => {
  const root = queryElement(document, "[data-pinned-root]", HTMLElement);
  if (!root) {
    return;
  }
  const list = queryElement(root, "[data-pinned-list]", HTMLElement);
  const statusEl = queryElement(root, "[data-pinned-status]", HTMLElement);
  if (!list || !statusEl) {
    return;
  }

  const csrfToken = root.getAttribute("data-pinned-csrf") || "";
  const fetcher = deps.fetch || fetch;
  const reload = deps.reload || ((): void => window.location.reload());

  const setStatus = (message: string, kind: PinnedListStatusKind = ""): void => {
    setStatusText(statusEl, message || " ", kind, ["is-success", "is-error"]);
  };

  const rows = (): HTMLElement[] => queryAllElements(list, "[data-pinned-item]", HTMLElement);

  const post = async (request: NotePinFetchRequest, fallback: string): Promise<boolean> => {
    try {
      const resp = await fetcher(request.url, request.init);
      const payload = await readNotePinResponsePayload(resp);
      if (!resp.ok) {
        setStatus(normalizeManagementErrorMessage(resp.status, payload), "is-error");
        return false;
      }
      const result = payload as ManagementResult;
      setStatus(result && result.ok && result.message ? result.message : fallback, "is-success");
      return true;
    } catch {
      setStatus("Pin request failed.", "is-error");
      return false;
    }
  };

  let moveQueue: Promise<void> = Promise.resolve();
  let queuedMoves = 0;
  let resyncing = false;

  const sendMove = async (move: PendingPinnedMove): Promise<boolean> => {
    if (!move.key || !csrfToken) {
      setStatus("Reorder unavailable.", "is-error");
      return false;
    }
    setStatus("Saving pin order...");
    return post(
      buildNotePinReorderFetchRequest({
        key: move.key,
        token: csrfToken,
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
  const enqueueMove = (item: HTMLElement, restore: Element | null): void => {
    queuedMoves += 1;
    const sequence = queuedMoves;
    const neighbors = pinnedNeighborKeys(item);
    const move: PendingPinnedMove = {
      key: pinnedItemKey(item),
      before: neighbors.before,
      after: neighbors.after,
    };
    setStatus("Saving pin order...");
    moveQueue = moveQueue.then(async () => {
      if (resyncing) {
        return;
      }
      if (await sendMove(move)) {
        return;
      }
      if (sequence === queuedMoves) {
        list.insertBefore(item, restore);
        return;
      }
      resyncing = true;
      reload();
    });
  };

  const moveByKeyboard = (item: HTMLElement, direction: -1 | 1): void => {
    const restore = item.nextElementSibling;
    if (direction < 0) {
      const previous = item.previousElementSibling;
      if (!previous) {
        return;
      }
      list.insertBefore(item, previous);
    } else {
      const next = item.nextElementSibling;
      if (!next) {
        return;
      }
      list.insertBefore(item, next.nextElementSibling);
    }
    enqueueMove(item, restore);
  };

  const startDrag = (item: HTMLElement, event: PointerEvent): void => {
    if (event.button !== 0) {
      return;
    }
    event.preventDefault();
    const restore = item.nextElementSibling;
    item.classList.add(draggingClass);

    const clearDropTarget = (): void => {
      for (const row of rows()) {
        row.classList.remove(dropTargetClass);
      }
    };

    const onPointerMove = (moveEvent: PointerEvent): void => {
      const next = rows().find((row) => {
        if (row === item) {
          return false;
        }
        const rect = row.getBoundingClientRect();
        return moveEvent.clientY < rect.top + rect.height / 2;
      });
      clearDropTarget();
      if (next) {
        next.classList.add(dropTargetClass);
        list.insertBefore(item, next);
      } else {
        list.appendChild(item);
      }
    };

    const finishDrag = (): void => {
      item.classList.remove(draggingClass);
      clearDropTarget();
      window.removeEventListener("pointermove", onPointerMove);
      window.removeEventListener("pointerup", finishDrag);
      window.removeEventListener("pointercancel", finishDrag);
      if (item.nextElementSibling === restore) {
        return;
      }
      enqueueMove(item, restore);
    };

    window.addEventListener("pointermove", onPointerMove);
    window.addEventListener("pointerup", finishDrag, { once: true });
    window.addEventListener("pointercancel", finishDrag, { once: true });
  };

  // A row that leaves the list changes nothing about the remaining ranks, but
  // the page is server-rendered, so a reload is what restores the empty state
  // and the numbering.
  const unpin = async (item: HTMLElement, button: HTMLButtonElement): Promise<void> => {
    const key = pinnedItemKey(item);
    if (!key || !csrfToken) {
      setStatus("Unpin unavailable.", "is-error");
      return;
    }
    button.disabled = true;
    setStatus("Unpinning note...");
    const ok = await post(
      buildNotePinRemoveFetchRequest({ key, token: csrfToken }),
      "Unpinned note."
    );
    button.disabled = false;
    if (ok) {
      reload();
    }
  };

  for (const item of rows()) {
    const handle = queryElement(item, "[data-pinned-handle]", HTMLButtonElement);
    if (handle) {
      handle.addEventListener("pointerdown", (event) => startDrag(item, event));
      handle.addEventListener("keydown", (event) => {
        if (event.key !== "ArrowUp" && event.key !== "ArrowDown") {
          return;
        }
        event.preventDefault();
        moveByKeyboard(item, event.key === "ArrowUp" ? -1 : 1);
      });
    }

    const pinButton = queryElement(item, "[data-pin-button]", HTMLButtonElement);
    if (pinButton) {
      pinButton.addEventListener("click", () => {
        void unpin(item, pinButton);
      });
    }
  }
};
