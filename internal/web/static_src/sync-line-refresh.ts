import type { SyncStatusResult } from "./api-types";
import { managementMutationEventName } from "./api-notes";
import { confirmedWriteEvent } from "./write-notice";
import type { SyncOutcome } from "./sync-requests";

const interval = 2000;
const maxReads = 30;

const pending = (status: SyncStatusResult): boolean =>
  status.export.pending_commits > 0 || status.export.baseline_pending || status.export.reserved ||
  status.transport.awaiting_receipt > 0 || status.transport.incomplete > 0 ||
  status.peer.awaiting_application > 0 || status.peer.incomplete > 0 || status.peer.pending ||
  status.workers.some((worker) => worker.failures > 0);

type RefreshOptions = {
  doc: Document;
  line: HTMLElement;
  read: () => Promise<SyncOutcome<SyncStatusResult>>;
  render: (outcome: SyncOutcome<SyncStatusResult>) => void;
};

// At most one minute of follow-up reads per visit or mutation. Hidden pages
// stop immediately. A mutation invalidates any outstanding pre-mutation read.
export const bindSyncRefresh = (options: RefreshOptions): (() => void) => {
  const { doc, line } = options;
  const view = doc.defaultView;
  if (!view) return () => {};
  let generation = 0;
  let remaining = 0;
  let grace = 0;
  let timer: number | undefined;
  let disposed = false;
  const stop = (): void => {
    generation++;
    view.clearTimeout(timer);
  };
  const visible = (): boolean => !disposed && line.isConnected && doc.visibilityState !== "hidden";
  const read = async (): Promise<void> => {
    if (!visible()) return;
    const request = ++generation;
    const outcome = await options.read();
    if (request !== generation || !visible()) return;
    options.render(outcome);
    if (!outcome.ok && ["forbidden", "not_found"].includes(outcome.code)) return;
    const retry = outcome.ok ? pending(outcome.result) : true;
    if (remaining-- > 0 && (retry || grace-- > 0)) timer = view.setTimeout(() => void read(), interval);
  };
  const refresh = (event?: Event): void => {
    stop();
    remaining = maxReads - 1;
    // A new save can precede the companion's next pass. Read for another ten
    // seconds even if its first answer still reports the previous idle state.
    grace = event?.type === confirmedWriteEvent || event?.type === managementMutationEventName ? 5 : 0;
    void read();
  };
  const visibility = (): void => { if (visible()) refresh(); else stop(); };
  view.addEventListener(confirmedWriteEvent, refresh);
  view.addEventListener(managementMutationEventName, refresh);
  view.addEventListener("pagehide", stop);
  view.addEventListener("pageshow", refresh);
  doc.addEventListener("visibilitychange", visibility);
  refresh();
  return () => {
    disposed = true;
    stop();
    view.removeEventListener(confirmedWriteEvent, refresh);
    view.removeEventListener(managementMutationEventName, refresh);
    view.removeEventListener("pagehide", stop);
    view.removeEventListener("pageshow", refresh);
    doc.removeEventListener("visibilitychange", visibility);
  };
};
