import { createFakeSurfaceFactory, type FakeSurface } from "./source-editor.test-helpers";
import { memoryDraftStorage, type FakeDraftStorage } from "./source-draft.test-helpers";
import { createSyncConflictsPanel, type SyncConflictsPanel } from "./sync-conflicts-panel";
import {
  syncConflictsMarkup,
  syncJSON,
  syncOperation,
  syncPlanPayload,
  syncResolvedPayload,
} from "./sync.test-helpers";
import type { SyncConflictOperation } from "./api";
import type { FetchLike } from "./sync-requests";

// The harness the conflict review controller is driven through.
//
// It is a server and a mounted page rather than a pile of mocks because what the
// controller has to get right is a *sequence*: the holds are read, a decision is
// planned, the plan is confirmed, the receipt is redeemed once, and the page reads
// again. Asserting on `calls` is how a test says "and nothing else was written".

export type SyncCall = { url: string; body: Record<string, unknown> | null };

type SyncServerState = {
  // holds is what the conflicts route answers. A test that resolves something
  // empties it, the way a vault does.
  holds: SyncConflictOperation[];
  conflicts: () => Response;
  preview: () => Response;
  resolve: () => Response;
};

export type SyncServer = {
  state: SyncServerState;
  fetch: FetchLike;
  calls: SyncCall[];
  urls(): string[];
  /** lastBody is the newest request to a route, so a test can read what was sent. */
  lastBody(suffix: string): Record<string, unknown> | null;
};

export const createSyncServer = (): SyncServer => {
  const calls: SyncCall[] = [];
  const state: SyncServerState = {
    holds: [syncOperation()],
    conflicts: (): Response => syncJSON({ ok: true, operations: state.holds }),
    preview: (): Response => syncJSON(syncPlanPayload()),
    resolve: (): Response => syncJSON(syncResolvedPayload()),
  };

  const fetch: FetchLike = (url, init) => {
    calls.push({
      url,
      body: init?.body ? (JSON.parse(String(init.body)) as Record<string, unknown>) : null,
    });
    if (url.endsWith("/preview")) {
      return Promise.resolve(state.preview());
    }
    if (url.endsWith("/resolve")) {
      return Promise.resolve(state.resolve());
    }
    return Promise.resolve(state.conflicts());
  };

  return {
    state,
    fetch,
    calls,
    urls: (): string[] => calls.map((call) => call.url),
    lastBody: (suffix: string): Record<string, unknown> | null =>
      [...calls].reverse().find((call) => call.url.endsWith(suffix))?.body ?? null,
  };
};

export type SyncPanelHarness = {
  panel: SyncConflictsPanel;
  root: HTMLElement;
  /** surface is the newest merge editor's text surface, or null before one is opened. */
  surface(): FakeSurface | null;
  storage: FakeDraftStorage;
  confirmations: string[];
  /** answerConfirm sets what the next confirmation dialog is answered with. */
  answerConfirm(answer: boolean): void;
  /** changes counts the times the page asked the status panel to read again. */
  changes(): number;
};

export const mountSyncConflicts = (
  server: SyncServer,
  options: { vaultID?: string; storage?: FakeDraftStorage } = {}
): SyncPanelHarness => {
  document.body.innerHTML = syncConflictsMarkup(options.vaultID ?? "sync:vault-under-test");
  const root = document.querySelector<HTMLElement>("[data-sync-conflicts]");
  if (!root) {
    throw new Error("the fixture markup carries no conflict panel");
  }
  const storage = options.storage ?? memoryDraftStorage();
  const { factory, surface } = createFakeSurfaceFactory();
  const confirmations: string[] = [];
  let answer = true;
  let changes = 0;

  const panel = createSyncConflictsPanel({
    root,
    fetch: server.fetch,
    surface: () => factory,
    storage,
    // The tab identity is given rather than resolved, so the drafts under test are
    // this test's own rather than whatever sessionStorage jsdom happens to hold.
    draftID: "tab-1",
    confirm: (message: string): boolean => {
      confirmations.push(message);
      return answer;
    },
    changed: (): void => {
      changes += 1;
    },
    view: window,
  });
  if (!panel) {
    throw new Error("the panel refused the fixture markup");
  }

  return {
    panel,
    root,
    surface,
    storage,
    confirmations,
    answerConfirm: (next: boolean): void => {
      answer = next;
    },
    changes: (): number => changes,
  };
};

const required = <T extends Element>(selector: string): T => {
  const found = document.querySelector<T>(selector);
  if (!found) {
    throw new Error(`the rendered page has no ${selector}`);
  }
  return found;
};

/** chooseSyncPath takes a decision the way a person does: by checking the radio. */
export const chooseSyncPath = (path: string, choice: string): void => {
  const radio = required<HTMLInputElement>(
    `[data-sync-choices="${path}"] [data-sync-choice="${choice}"]`
  );
  radio.checked = true;
  radio.dispatchEvent(new Event("change"));
};

export const syncAction = (action: "review" | "apply" | "cancel"): HTMLButtonElement =>
  required<HTMLButtonElement>(`[data-sync-${action}]`);

export const syncPanelStatus = (): HTMLElement => required("[data-sync-conflicts-status]");
export const syncOperationStatus = (): HTMLElement => required("[data-sync-operation-status]");
export const syncPlanBox = (): HTMLElement => required("[data-sync-plan]");
export const syncMergePane = (path = "Alpha.md"): HTMLElement =>
  required(`[data-sync-merge="${path}"]`);
export const syncMergeNotice = (path = "Alpha.md"): HTMLElement =>
  required(`[data-sync-merge-notice="${path}"]`);
