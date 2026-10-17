import { announceConfirmedWrite } from "./write-notice";
import { responseFailure, unansweredRequest, type RequestPurpose } from "./request-outcome";
import {
  SYNC_CONFLICTS_ENDPOINT,
  SYNC_PREVIEW_ENDPOINT,
  SYNC_RESOLVE_ENDPOINT,
  SYNC_STATUS_ENDPOINT,
  buildSyncRequestInit,
  normalizeManagementErrorMessage,
  normalizeSyncConflictsResponse,
  normalizeSyncPlanResponse,
  normalizeSyncResolvedResponse,
  normalizeSyncStatusResponse,
  type SyncConflictsResult,
  type SyncPathDecision,
  type SyncPlanResult,
  type SyncRequestBody,
  type SyncResolvedResult,
  type SyncStatusResult,
} from "./api";
import { asRecord } from "./api-internal";
import { normalizeParamValue } from "./filters";

export type FetchLike = (input: string, init?: RequestInit) => Promise<Response>;

// SyncOutcome carries the server's typed code beside the message, which the
// configuration surfaces do not need and this one does: a rejected resolution is
// either "the vault moved on" (preview_stale, re-read and decide again, the draft
// is still yours) or "this vault will not write right now" (a safety refusal,
// where deciding again would be the wrong advice). Without the code the page would
// have to guess which from English.
export type SyncFailure = { ok: false; code: string; message: string };

export type SyncOutcome<T> = { ok: true; result: T } | SyncFailure;

/** stalePreview is the one refusal that means "read both versions again". */
export const stalePreview = (failure: SyncFailure): boolean =>
  failure.code === "preview_stale" || failure.code === "preview_required";

const readPayload = async (resp: Response): Promise<unknown> => {
  try {
    return await resp.json();
  } catch {
    return null;
  }
};

const payloadCode = (payload: unknown): string =>
  normalizeParamValue(asRecord(payload)?.code);

const finish = async <T extends { ok: boolean; code?: string; message?: string }>(
  resp: Response,
  normalize: (payload: unknown) => T,
  purpose: RequestPurpose = "read"
): Promise<SyncOutcome<T>> => {
  const payload = await readPayload(resp);
  const failure = responseFailure(resp, payload, purpose);
  if (failure?.uncertain) return { ...failure, code: "unconfirmed" };
  if (!resp.ok) {
    return {
      ok: false,
      code: payloadCode(payload),
      message: normalizeManagementErrorMessage(resp.status, payload),
    };
  }
  const result = normalize(payload);
  if (!result.ok) {
    return {
      ok: false,
      code: result.code || payloadCode(payload),
      message: result.message || "The request was refused.",
    };
  }
  if (purpose === "write") announceConfirmedWrite();
  return { ok: true, result };
};

const get = async <T extends { ok: boolean; code?: string; message?: string }>(
  fetcher: FetchLike,
  endpoint: string,
  normalize: (payload: unknown) => T
): Promise<SyncOutcome<T>> => {
  try {
    const resp = await fetcher(endpoint, { headers: { Accept: "application/json" } });
    return await finish(resp, normalize);
  } catch {
    return { ok: false, code: "", message: "The server did not answer." };
  }
};

const post = async <T extends { ok: boolean; code?: string; message?: string }>(
  fetcher: FetchLike,
  endpoint: string,
  token: string,
  body: SyncRequestBody,
  normalize: (payload: unknown) => T,
  purpose: RequestPurpose = "preview"
): Promise<SyncOutcome<T>> => {
  try {
    const resp = await fetcher(endpoint, buildSyncRequestInit(token, body));
    return await finish(resp, normalize, purpose);
  } catch {
    return { ...unansweredRequest(purpose), code: purpose === "write" ? "unconfirmed" : "" };
  }
};

/** Reads where synchronization has got to. Read-only, so it carries no token. */
export const fetchSyncStatus = (
  fetcher: FetchLike,
  endpoint: string = SYNC_STATUS_ENDPOINT
): Promise<SyncOutcome<SyncStatusResult>> =>
  get(fetcher, endpoint, normalizeSyncStatusResponse);

/** Reads every conflict this vault is holding. Read-only, so it carries no token. */
export const fetchSyncConflicts = (
  fetcher: FetchLike,
  endpoint: string = SYNC_CONFLICTS_ENDPOINT
): Promise<SyncOutcome<SyncConflictsResult>> =>
  get(fetcher, endpoint, normalizeSyncConflictsResponse);

/**
 * Plans a resolution and takes the one-use receipt the resolve route redeems.
 *
 * Every contested path of the package has to be decided in one request: the
 * server refuses a partial decision, because a path nobody named is a path nobody
 * looked at and inheriting a destructive choice for it is the mistake this whole
 * surface exists to prevent.
 */
export const previewSyncResolution = (
  fetcher: FetchLike,
  endpoint: string,
  token: string,
  packageID: string,
  paths: SyncPathDecision[]
): Promise<SyncOutcome<SyncPlanResult>> =>
  post(
    fetcher,
    endpoint || SYNC_PREVIEW_ENDPOINT,
    token,
    { token, package_id: packageID, paths },
    normalizeSyncPlanResponse
  );

export const applySyncResolution = (
  fetcher: FetchLike,
  endpoint: string,
  token: string,
  previewID: string
): Promise<SyncOutcome<SyncResolvedResult>> =>
  post(
    fetcher,
    endpoint || SYNC_RESOLVE_ENDPOINT,
    token,
    { preview_id: previewID },
    normalizeSyncResolvedResponse,
    "write"
  );
