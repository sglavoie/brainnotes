import type {
  SyncBlocked,
  SyncChoice,
  SyncConflictOperation,
  SyncContestedPath,
  SyncExport,
  SyncPeer,
  SyncPlanResult,
  SyncResolvedResult,
  SyncSave,
  SyncStatusResult,
  SyncTransport,
  SyncVersion,
  SyncWorker,
} from "./api";

// Fixtures for the synchronization page's jsdom tests.
//
// The markup below is a second expression of internal/web/templates/sync.html,
// for the reason source-editor.test-helpers.ts gives: a Node process cannot
// render a Go template. The *attribute names* are the contract between them and
// they are pinned on the Go side by internal/web/sync_page_test.go, which asserts
// the served page carries every one of them — so a renamed attribute fails there
// rather than passing here against markup nobody is served.
//
// The payload builders default to the case that needs the fewest words: one text
// conflict on one path, every choice on offer, nothing blocked behind it. Each
// test says only what makes its own situation different, which is why an
// assertion about binaries or deletions reads as one line rather than a wall of
// fields.

export const syncTextVersion = (text: string, overrides: Partial<SyncVersion> = {}): SyncVersion => ({
  exists: true,
  sha256: `sha-${text.trim()}`,
  shown: true,
  text,
  size: text.length,
  binary: false,
  too_large: false,
  ...overrides,
});

/** syncBinaryVersion exists and is not shown, which is the pair that matters. */
export const syncBinaryVersion = (size = 4096): SyncVersion => ({
  exists: true,
  sha256: "sha-binary",
  shown: false,
  text: "",
  size,
  binary: true,
  too_large: false,
});

export const syncMissingVersion = (): SyncVersion => ({
  exists: false,
  sha256: "",
  shown: false,
  text: "",
  size: 0,
  binary: false,
  too_large: false,
});

export const syncChoice = (choice: string, overrides: Partial<SyncChoice> = {}): SyncChoice => ({
  choice,
  label: `Choose ${choice}`,
  effect: `What ${choice} does.`,
  discards: "",
  reason: "",
  ...overrides,
});

export const keepCurrent = (): SyncChoice => syncChoice("keep-current", { discards: "remote" });
export const acceptIncoming = (): SyncChoice => syncChoice("accept-incoming", { discards: "local" });
export const keepBoth = (): SyncChoice => syncChoice("keep-both");
export const manualMergeChoice = (): SyncChoice => syncChoice("manual-merge");

export const syncContestedPath = (overrides: Partial<SyncContestedPath> = {}): SyncContestedPath => ({
  path: "Alpha.md",
  kind: "concurrent-edit",
  summary: "Both replicas changed this note.",
  renamed_to: "",
  collides: "",
  local: syncTextVersion("mine\n"),
  remote: syncTextVersion("theirs\n"),
  baseline: syncTextVersion("both\n"),
  local_changed: false,
  available: [keepCurrent(), acceptIncoming(), keepBoth(), manualMergeChoice()],
  refused: [],
  mergeable: true,
  remedy: "",
  ...overrides,
});

export const syncBlocked = (overrides: Partial<SyncBlocked> = {}): SyncBlocked => ({
  package_id: "pkg-2",
  origin_device: "device-b",
  sequence: 5,
  reason: "predecessor",
  detail: "waiting for operation op-1",
  waiting_on: "pkg-1",
  ...overrides,
});

export const syncOperation = (
  overrides: Partial<SyncConflictOperation> = {}
): SyncConflictOperation => ({
  package_id: "pkg-1",
  operation_id: "op-1",
  origin_device: "device-b",
  sequence: 4,
  detected_at: "2026-02-03T10:00:00Z",
  paths: [syncContestedPath()],
  blocked: [],
  problem: "",
  ...overrides,
});

export const syncWorker = (overrides: Partial<SyncWorker> = {}): SyncWorker => ({
  name: "export",
  failures: 0,
  last_error: "",
  last_run_at: "",
  last_success_at: "",
  next_attempt_after: "",
  ready: true,
  ...overrides,
});

// SyncStatusOverrides takes the four counted sections as partials, because a test
// about acknowledgment should name published and acknowledged and nothing else.
type SyncStatusOverrides = Omit<
  Partial<SyncStatusResult>,
  "save" | "export" | "transport" | "peer"
> & {
  save?: Partial<SyncSave>;
  export?: Partial<SyncExport>;
  transport?: Partial<SyncTransport>;
  peer?: Partial<SyncPeer>;
};

export const syncStatus = (overrides: SyncStatusOverrides = {}): SyncStatusResult => {
  const { save, export: exported, transport, peer, ...rest } = overrides;
  return {
    ok: true,
    mode: "local",
    enrolled: true,
    observed_at: "2026-02-03T10:05:00Z",
    headline: "idle",
    states: ["idle"],
    summary: "Everything is up to date.",
    needs_person: false,
    save: {
      managed: true,
      saved_at: "",
      last_commit_at: "2026-02-03T09:00:00Z",
      blocked: false,
      block_reason: "",
      block_recovery: [],
      ...save,
    },
    export: { pending_commits: 0, baseline_pending: false, reserved: false, ...exported },
    transport: {
      published: 0,
      acknowledged: 0,
      awaiting_receipt: 0,
      incomplete: 0,
      oldest_awaiting: "",
      rejected: [],
      ...transport,
    },
    peer: {
      delivered: 0,
      awaiting_application: 0,
      incomplete: 0,
      applied: 0,
      pending: false,
      last_delivery_at: "",
      conflicts: 0,
      ...peer,
    },
    workers: [],
    problems: [],
    replica: null,
    review_path: "",
    ...rest,
  };
};

export const syncPlanPayload = (overrides: Partial<SyncPlanResult> = {}): SyncPlanResult => ({
  ok: true,
  preview_id: "preview-1",
  expires_at: "2026-02-03T10:10:00Z",
  package_id: "pkg-1",
  operation_id: "op-1",
  paths: [
    {
      path: "Alpha.md",
      kind: "concurrent-edit",
      choice: "keep-current",
      label: "Keep this vault's version",
      steps: [{ path: "Alpha.md", action: "write", sha256: "sha-mine" }],
    },
  ],
  ...overrides,
});

export const syncResolvedPayload = (
  overrides: Partial<SyncResolvedResult> = {}
): SyncResolvedResult => ({
  ok: true,
  package_id: "pkg-1",
  operation_id: "op-1",
  journal_id: "entry-1",
  paths: ["Alpha.md"],
  acknowledged: true,
  abandoned: [],
  summary: "Alpha.md now holds your version, and your decision was recorded.",
  ...overrides,
});

export const syncStatusPanelMarkup = (): string => `
<section
  class="panel sync-panel"
  data-sync-panel
  data-sync-status-endpoint="/api/sync/status"
>
  <button type="button" data-sync-refresh>Refresh</button>
  <p class="sync-status" data-sync-summary role="status"></p>
  <time data-sync-checked hidden></time>
  <details data-sync-details><summary>Synchronization details</summary>
  <dl class="sync-facts" data-sync-facts></dl></details>
  <ul class="sync-problems" data-sync-problems></ul>
</section>
`;

// syncConflictsMarkup takes the vault identity as an argument because a page
// served without one keeps no merge in this browser, and that is a case under
// test rather than a variant of the markup.
export const syncConflictsMarkup = (vaultID = "sync:vault-under-test"): string => `
<section
  class="panel sync-conflicts-panel"
  data-sync-conflicts
  data-sync-conflicts-endpoint="/api/sync/conflicts"
  data-sync-preview-endpoint="/api/sync/conflicts/preview"
  data-sync-resolve-endpoint="/api/sync/conflicts/resolve"
  data-sync-csrf="csrf-token"
  ${vaultID === "" ? "" : `data-sync-vault="${vaultID}"`}
>
  <p class="sync-status" data-sync-conflicts-status role="status"></p>
  <div class="sync-operations" data-sync-operations></div>
</section>
`;

export const syncLineMarkup = (endpoint = "/api/sync/status"): string =>
  `<p data-sync-line data-sync-status-endpoint="${endpoint}"></p>`;

export const syncJSON = (payload: unknown, status = 200): Response =>
  new Response(JSON.stringify(payload), {
    status,
    headers: { "Content-Type": "application/json" },
  });
