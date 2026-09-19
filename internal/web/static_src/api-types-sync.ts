// Synchronization status and conflict review transport ------------------------
//
// These mirror internal/web/sync_status.go, sync_conflicts.go and
// sync_resolve.go. Two shapes here are load-bearing rather than incidental.
//
// A version says separately whether it `exists` and whether it is `shown`: a
// deletion has no content by definition, and a version that is binary or over the
// server's bound exists and cannot be carried. A page that read an empty `text`
// as "empty note" would offer to overwrite an attachment with nothing.
//
// A choice is carried whether or not it is on offer. `available` and `refused`
// come from the engine probing its own planner, so the page never decides what a
// conflict can take — it only renders the answer, refusals included, because "keep
// both is not possible here, and here is why" is the sentence somebody needs.

export type SyncVersion = {
  exists: boolean;
  sha256: string;
  shown: boolean;
  text: string;
  size: number;
  binary: boolean;
  too_large: boolean;
};

// SyncChoice is one of the four decisions. `discards` names the version that
// stops being in the vault — "local", "remote", or empty for a choice that keeps
// both — and `reason` is filled only on a refused one.
export type SyncChoice = {
  choice: string;
  label: string;
  effect: string;
  discards: string;
  reason: string;
};

export type SyncContestedPath = {
  path: string;
  kind: string;
  summary: string;
  renamed_to: string;
  collides: string;
  local: SyncVersion;
  remote: SyncVersion;
  baseline: SyncVersion;
  local_changed: boolean;
  available: SyncChoice[];
  refused: SyncChoice[];
  mergeable: boolean;
  remedy: string;
};

// SyncBlocked is one package waiting behind a conflict. `waiting_on` is the
// package it directly waits on, so a chain reads as a chain.
export type SyncBlocked = {
  package_id: string;
  origin_device: string;
  sequence: number;
  reason: string;
  detail: string;
  waiting_on: string;
};

export type SyncConflictOperation = {
  package_id: string;
  operation_id: string;
  origin_device: string;
  sequence: number;
  detected_at: string;
  paths: SyncContestedPath[];
  blocked: SyncBlocked[];
  problem: string;
};

export type SyncConflictsResult = {
  ok: boolean;
  code?: string;
  message?: string;
  operations: SyncConflictOperation[];
};

export type SyncSave = {
  managed: boolean;
  saved_at: string;
  last_commit_at: string;
  blocked: boolean;
  block_reason: string;
  block_recovery: string[];
};

export type SyncExport = {
  pending_commits: number;
  baseline_pending: boolean;
  reserved: boolean;
};

export type SyncReceiptProblem = {
  package_id: string;
  detail: string;
};

export type SyncTransport = {
  published: number;
  acknowledged: number;
  awaiting_receipt: number;
  incomplete: number;
  oldest_awaiting: string;
  rejected: SyncReceiptProblem[];
};

export type SyncPeer = {
  delivered: number;
  awaiting_application: number;
  incomplete: number;
  applied: number;
  pending: boolean;
  last_delivery_at: string;
  conflicts: number;
};

export type SyncProblem = {
  subject: string;
  detail: string;
};

export type SyncWorker = {
  name: string;
  failures: number;
  last_error: string;
  last_run_at: string;
  last_success_at: string;
  next_attempt_after: string;
  ready: boolean;
};

// SyncReplica is the freshness of a published read-only copy, and is present only
// in the mode that has one.
export type SyncReplica = {
  recorded: boolean;
  stale: boolean;
  last_successful_sync: string;
};

export type SyncStatusResult = {
  ok: boolean;
  code?: string;
  message?: string;
  mode: string;
  enrolled: boolean;
  observed_at: string;
  headline: string;
  states: string[];
  summary: string;
  needs_person: boolean;
  save: SyncSave;
  export: SyncExport;
  transport: SyncTransport;
  peer: SyncPeer;
  workers: SyncWorker[];
  problems: SyncProblem[];
  replica: SyncReplica | null;
  review_path: string;
};

// SyncPathDecision is one path's decision. `merged` is a string only for a manual
// merge, and null otherwise — an empty merge is a legitimate decision and is not
// the same as not having supplied one, which is why the server takes a pointer.
export type SyncPathDecision = {
  path: string;
  choice: string;
  merged: string | null;
};

export type SyncPreviewRequest = {
  token: string;
  package_id: string;
  paths: SyncPathDecision[];
};

export type SyncResolveApplyRequest = {
  preview_id: string;
};

export type SyncRequestBody = SyncPreviewRequest | SyncResolveApplyRequest;

export type SyncPlanStep = {
  path: string;
  action: string;
  sha256: string;
};

export type SyncPlanPath = {
  path: string;
  kind: string;
  choice: string;
  steps: SyncPlanStep[];
};

export type SyncPlanResult = {
  ok: boolean;
  code?: string;
  message?: string;
  preview_id: string;
  expires_at: string;
  package_id: string;
  operation_id: string;
  paths: SyncPlanPath[];
};

export type SyncAbandoned = {
  package_id: string;
  reason: string;
};

export type SyncResolvedResult = {
  ok: boolean;
  code?: string;
  message?: string;
  package_id: string;
  operation_id: string;
  journal_id: string;
  paths: string[];
  // acknowledged is whether the other replica has been told. A resolution is
  // complete in this vault without it, and the peer keeps offering the package
  // until an acknowledgment reaches it, so the page says which happened.
  acknowledged: boolean;
  abandoned: SyncAbandoned[];
  summary: string;
};
