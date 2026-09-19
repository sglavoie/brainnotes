import { normalizeParamValue } from "./filters";
import {
  asRecord,
  normalizeManagementResult,
  normalizeStringArray,
  readBoolean,
  readCount,
} from "./api-internal";
import type {
  SyncAbandoned,
  SyncBlocked,
  SyncChoice,
  SyncConflictOperation,
  SyncConflictsResult,
  SyncContestedPath,
  SyncExport,
  SyncPeer,
  SyncPlanPath,
  SyncPlanResult,
  SyncPlanStep,
  SyncProblem,
  SyncReceiptProblem,
  SyncReplica,
  SyncRequestBody,
  SyncResolvedResult,
  SyncSave,
  SyncStatusResult,
  SyncTransport,
  SyncVersion,
  SyncWorker,
} from "./api-types";

export const SYNC_STATUS_ENDPOINT = "/api/sync/status";
export const SYNC_CONFLICTS_ENDPOINT = "/api/sync/conflicts";
export const SYNC_PREVIEW_ENDPOINT = "/api/sync/conflicts/preview";
export const SYNC_RESOLVE_ENDPOINT = "/api/sync/conflicts/resolve";

// rawText keeps a version's bytes exactly as they were served. Every other field
// here goes through normalizeParamValue, which trims: a path or a digest with
// surrounding space is a parameter nobody typed, while a note whose leading blank
// line was trimmed away is a merge that would silently rewrite the file.
const rawText = (value: unknown): string => (typeof value === "string" ? value : "");

const normalizeSyncVersion = (input: unknown): SyncVersion => {
  const record = asRecord(input) || {};
  return {
    exists: readBoolean(record.exists),
    sha256: normalizeParamValue(record.sha256),
    shown: readBoolean(record.shown),
    text: rawText(record.text),
    size: readCount(record.size),
    binary: readBoolean(record.binary),
    too_large: readBoolean(record.too_large),
  };
};

const normalizeSyncChoice = (input: unknown): SyncChoice | null => {
  const record = asRecord(input);
  if (!record) {
    return null;
  }
  const choice = normalizeParamValue(record.choice);
  if (choice === "") {
    return null;
  }
  return {
    choice,
    label: normalizeParamValue(record.label) || choice,
    effect: normalizeParamValue(record.effect),
    discards: normalizeParamValue(record.discards),
    reason: normalizeParamValue(record.reason),
  };
};

const normalizeSyncChoices = (input: unknown): SyncChoice[] =>
  Array.isArray(input)
    ? input.flatMap((item) => {
        const choice = normalizeSyncChoice(item);
        return choice ? [choice] : [];
      })
    : [];

// normalizeSyncContestedPath drops a path with no name. Every decision this page
// can submit names its path, so a nameless one could only produce a request the
// server refuses for a reason nobody reading the page could act on.
const normalizeSyncContestedPath = (input: unknown): SyncContestedPath | null => {
  const record = asRecord(input);
  if (!record) {
    return null;
  }
  const path = normalizeParamValue(record.path);
  if (path === "") {
    return null;
  }
  return {
    path,
    kind: normalizeParamValue(record.kind),
    summary: normalizeParamValue(record.summary),
    renamed_to: normalizeParamValue(record.renamed_to),
    collides: normalizeParamValue(record.collides),
    local: normalizeSyncVersion(record.local),
    remote: normalizeSyncVersion(record.remote),
    baseline: normalizeSyncVersion(record.baseline),
    local_changed: readBoolean(record.local_changed),
    available: normalizeSyncChoices(record.available),
    refused: normalizeSyncChoices(record.refused),
    mergeable: readBoolean(record.mergeable),
    remedy: normalizeParamValue(record.remedy),
  };
};

const normalizeSyncBlocked = (input: unknown): SyncBlocked | null => {
  const record = asRecord(input);
  const packageID = record ? normalizeParamValue(record.package_id) : "";
  if (!record || packageID === "") {
    return null;
  }
  return {
    package_id: packageID,
    origin_device: normalizeParamValue(record.origin_device),
    sequence: readCount(record.sequence),
    reason: normalizeParamValue(record.reason),
    detail: normalizeParamValue(record.detail),
    waiting_on: normalizeParamValue(record.waiting_on),
  };
};

const normalizeSyncOperation = (input: unknown): SyncConflictOperation | null => {
  const record = asRecord(input);
  const packageID = record ? normalizeParamValue(record.package_id) : "";
  if (!record || packageID === "") {
    return null;
  }
  return {
    package_id: packageID,
    operation_id: normalizeParamValue(record.operation_id),
    origin_device: normalizeParamValue(record.origin_device),
    sequence: readCount(record.sequence),
    detected_at: normalizeParamValue(record.detected_at),
    paths: Array.isArray(record.paths)
      ? record.paths.flatMap((item) => {
          const path = normalizeSyncContestedPath(item);
          return path ? [path] : [];
        })
      : [],
    blocked: Array.isArray(record.blocked)
      ? record.blocked.flatMap((item) => {
          const blocked = normalizeSyncBlocked(item);
          return blocked ? [blocked] : [];
        })
      : [],
    problem: normalizeParamValue(record.problem),
  };
};

export const normalizeSyncConflictsResponse = (payload: unknown): SyncConflictsResult => {
  const base = normalizeManagementResult(payload);
  const record = asRecord(payload) || {};
  return {
    ok: base.ok,
    code: base.ok ? undefined : base.code,
    message: base.message,
    operations: Array.isArray(record.operations)
      ? record.operations.flatMap((item) => {
          const operation = normalizeSyncOperation(item);
          return operation ? [operation] : [];
        })
      : [],
  };
};

const normalizeSyncSave = (input: unknown): SyncSave => {
  const record = asRecord(input) || {};
  return {
    managed: readBoolean(record.managed),
    saved_at: normalizeParamValue(record.saved_at),
    last_commit_at: normalizeParamValue(record.last_commit_at),
    blocked: readBoolean(record.blocked),
    block_reason: normalizeParamValue(record.block_reason),
    block_recovery: normalizeStringArray(record.block_recovery),
  };
};

const normalizeSyncExport = (input: unknown): SyncExport => {
  const record = asRecord(input) || {};
  return {
    pending_commits: readCount(record.pending_commits),
    baseline_pending: readBoolean(record.baseline_pending),
    reserved: readBoolean(record.reserved),
  };
};

const normalizeReceiptProblems = (input: unknown): SyncReceiptProblem[] =>
  Array.isArray(input)
    ? input.flatMap((item) => {
        const record = asRecord(item);
        const packageID = record ? normalizeParamValue(record.package_id) : "";
        return record && packageID !== ""
          ? [{ package_id: packageID, detail: normalizeParamValue(record.detail) }]
          : [];
      })
    : [];

const normalizeSyncTransport = (input: unknown): SyncTransport => {
  const record = asRecord(input) || {};
  return {
    published: readCount(record.published),
    acknowledged: readCount(record.acknowledged),
    awaiting_receipt: readCount(record.awaiting_receipt),
    incomplete: readCount(record.incomplete),
    oldest_awaiting: normalizeParamValue(record.oldest_awaiting),
    rejected: normalizeReceiptProblems(record.rejected),
  };
};

const normalizeSyncPeer = (input: unknown): SyncPeer => {
  const record = asRecord(input) || {};
  return {
    delivered: readCount(record.delivered),
    awaiting_application: readCount(record.awaiting_application),
    incomplete: readCount(record.incomplete),
    applied: readCount(record.applied),
    pending: readBoolean(record.pending),
    last_delivery_at: normalizeParamValue(record.last_delivery_at),
    conflicts: readCount(record.conflicts),
  };
};

const normalizeSyncProblems = (input: unknown): SyncProblem[] =>
  Array.isArray(input)
    ? input.flatMap((item) => {
        const record = asRecord(item);
        const detail = record ? normalizeParamValue(record.detail) : "";
        return record && detail !== ""
          ? [{ subject: normalizeParamValue(record.subject), detail }]
          : [];
      })
    : [];

const normalizeSyncWorkers = (input: unknown): SyncWorker[] =>
  Array.isArray(input)
    ? input.flatMap((item) => {
        const record = asRecord(item);
        const name = record ? normalizeParamValue(record.name) : "";
        return record && name !== ""
          ? [
              {
                name,
                failures: readCount(record.failures),
                last_error: normalizeParamValue(record.last_error),
                last_run_at: normalizeParamValue(record.last_run_at),
                last_success_at: normalizeParamValue(record.last_success_at),
                next_attempt_after: normalizeParamValue(record.next_attempt_after),
                ready: readBoolean(record.ready),
              },
            ]
          : [];
      })
    : [];

// normalizeSyncReplica answers null for every mode that publishes no copy, which
// is what lets the page tell "this server has no replica" from "this replica has
// never recorded a copy" — two different sentences with two different remedies.
const normalizeSyncReplica = (input: unknown): SyncReplica | null => {
  const record = asRecord(input);
  if (!record) {
    return null;
  }
  return {
    recorded: readBoolean(record.recorded),
    stale: readBoolean(record.stale),
    last_successful_sync: normalizeParamValue(record.last_successful_sync),
  };
};

export const normalizeSyncStatusResponse = (payload: unknown): SyncStatusResult => {
  const base = normalizeManagementResult(payload);
  const record = asRecord(payload) || {};
  return {
    ok: base.ok,
    code: base.ok ? undefined : base.code,
    message: base.message,
    mode: normalizeParamValue(record.mode),
    enrolled: readBoolean(record.enrolled),
    observed_at: normalizeParamValue(record.observed_at),
    headline: normalizeParamValue(record.headline),
    states: normalizeStringArray(record.states),
    summary: normalizeParamValue(record.summary),
    needs_person: readBoolean(record.needs_person),
    save: normalizeSyncSave(record.save),
    export: normalizeSyncExport(record.export),
    transport: normalizeSyncTransport(record.transport),
    peer: normalizeSyncPeer(record.peer),
    workers: normalizeSyncWorkers(record.workers),
    problems: normalizeSyncProblems(record.problems),
    replica: normalizeSyncReplica(record.replica),
    review_path: normalizeParamValue(record.review_path),
  };
};

const normalizeSyncPlanSteps = (input: unknown): SyncPlanStep[] =>
  Array.isArray(input)
    ? input.flatMap((item) => {
        const record = asRecord(item);
        const path = record ? normalizeParamValue(record.path) : "";
        return record && path !== ""
          ? [
              {
                path,
                action: normalizeParamValue(record.action),
                sha256: normalizeParamValue(record.sha256),
              },
            ]
          : [];
      })
    : [];

const normalizeSyncPlanPaths = (input: unknown): SyncPlanPath[] =>
  Array.isArray(input)
    ? input.flatMap((item) => {
        const record = asRecord(item);
        const path = record ? normalizeParamValue(record.path) : "";
        return record && path !== ""
          ? [
              {
                path,
                kind: normalizeParamValue(record.kind),
                choice: normalizeParamValue(record.choice),
                steps: normalizeSyncPlanSteps(record.steps),
              },
            ]
          : [];
      })
    : [];

export const normalizeSyncPlanResponse = (payload: unknown): SyncPlanResult => {
  const base = normalizeManagementResult(payload);
  const record = asRecord(payload) || {};
  return {
    ok: base.ok,
    code: base.ok ? undefined : base.code,
    message: base.message,
    preview_id: normalizeParamValue(record.preview_id),
    expires_at: normalizeParamValue(record.expires_at),
    package_id: normalizeParamValue(record.package_id),
    operation_id: normalizeParamValue(record.operation_id),
    paths: normalizeSyncPlanPaths(record.paths),
  };
};

const normalizeSyncAbandoned = (input: unknown): SyncAbandoned[] =>
  Array.isArray(input)
    ? input.flatMap((item) => {
        const record = asRecord(item);
        const packageID = record ? normalizeParamValue(record.package_id) : "";
        return record && packageID !== ""
          ? [{ package_id: packageID, reason: normalizeParamValue(record.reason) }]
          : [];
      })
    : [];

export const normalizeSyncResolvedResponse = (payload: unknown): SyncResolvedResult => {
  const base = normalizeManagementResult(payload);
  const record = asRecord(payload) || {};
  return {
    ok: base.ok,
    code: base.ok ? undefined : base.code,
    message: base.message,
    package_id: normalizeParamValue(record.package_id),
    operation_id: normalizeParamValue(record.operation_id),
    journal_id: normalizeParamValue(record.journal_id),
    paths: normalizeStringArray(record.paths),
    acknowledged: readBoolean(record.acknowledged),
    abandoned: normalizeSyncAbandoned(record.abandoned),
    summary: normalizeParamValue(record.summary),
  };
};

// buildSyncRequestInit renders one JSON request for the two resolution routes,
// the way buildConfigRequestInit does for the configuration ones: the CSRF token
// travels in the header and in the body, and the server reads the header first.
export const buildSyncRequestInit = (token: string, body: SyncRequestBody): RequestInit => ({
  method: "POST",
  headers: {
    Accept: "application/json",
    "Content-Type": "application/json",
    "X-BN-CSRF": token,
  },
  body: JSON.stringify(body),
});
