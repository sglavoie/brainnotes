import {
  HISTORY_ENDPOINT,
  HISTORY_PREVIEW_ENDPOINT,
  UNDO_ENDPOINT,
  buildHistoryPreviewForm,
  buildHistoryQuery,
  buildUndoForm,
  normalizeHistoryPreview,
  normalizeHistoryResult,
  normalizeManagementErrorMessage,
  normalizeUndoConflicts,
  type HistoryPreviewResult,
  type HistoryQuery,
  type HistoryResult,
  type UndoConflict,
  type UndoRequest,
  type UndoResult,
} from "./api";
import { asRecord, readBoolean } from "./api-internal";
import { normalizeParamValue } from "./filters";

export type HistoryFetch = (input: string, init?: RequestInit) => Promise<Response>;

/**
 * HistoryFailure carries the server's typed code beside the message, for the
 * reason SyncFailure does: on this surface the code is what separates "the vault
 * moved on while you were reading — look again" (preview_stale, preview_required)
 * from "this restore would replace newer work" (conflict) from "this vault will
 * not write right now". Deciding that from English would be a guess.
 */
export type HistoryFailure = {
  ok: false;
  code: string;
  status: number;
  message: string;
  /**
   * conflicts is what the refusal itself named, and it is reported as such —
   * never as the list somebody consents to forcing over. A conflict reply names
   * the paths that refused; only the preview names everything the restore would
   * write. Confirming against this list would understate the change.
   */
  conflicts: UndoConflict[];
  /**
   * entryID is the entry the server decided the request was about, which is not
   * always the one the client named: "undo the latest change" is posted with no
   * identifier at all, and the refusal is the first thing that says which entry
   * it resolved to. Forcing afterwards has to name that entry — forcing "the
   * latest" again would re-resolve it against a journal that may have moved.
   * Empty when the reply carried none, which is what tells the caller forcing is
   * not offerable.
   */
  entryID: string;
};

export type HistoryOutcome<T> = { ok: true; result: T } | HistoryFailure;

/** stalePreview is the refusal that means "read it again and decide again". */
export const stalePreview = (failure: HistoryFailure): boolean =>
  failure.code === "preview_stale" || failure.code === "preview_required";

/** conflicted is the refusal a non-forced restore gets when newer work is in the
 * way. It is not an error to report and stop on — it is the point at which the
 * person has to be shown what forcing would replace. */
export const conflicted = (failure: HistoryFailure): boolean => failure.code === "conflict";

const readPayload = async (resp: Response): Promise<unknown> => {
  const contentType = resp.headers?.get("Content-Type") || "";
  try {
    if (contentType.includes("application/json")) {
      return await resp.json();
    }
    return await resp.text();
  } catch {
    return null;
  }
};

const failure = (resp: Response, payload: unknown): HistoryFailure => ({
  ok: false,
  code: normalizeParamValue(asRecord(payload)?.code),
  status: resp.status,
  message: normalizeManagementErrorMessage(resp.status, payload),
  conflicts: normalizeUndoConflicts(asRecord(payload)?.conflicts),
  entryID: normalizeParamValue(asRecord(payload)?.entry_id),
});

const finish = async <T extends { ok: boolean; code?: string; message?: string }>(
  resp: Response,
  normalize: (payload: unknown) => T
): Promise<HistoryOutcome<T>> => {
  const payload = await readPayload(resp);
  if (!resp.ok) {
    return failure(resp, payload);
  }
  const result = normalize(payload);
  if (!result.ok) {
    return {
      ok: false,
      code: result.code || normalizeParamValue(asRecord(payload)?.code),
      status: resp.status,
      message: result.message || "The request was refused.",
      conflicts: normalizeUndoConflicts(asRecord(payload)?.conflicts),
      entryID: normalizeParamValue(asRecord(payload)?.entry_id),
    };
  }
  return { ok: true, result };
};

const unreachable = (): HistoryFailure => ({
  ok: false,
  code: "",
  status: 0,
  message: "The server did not answer.",
  conflicts: [],
  entryID: "",
});

const formInit = (token: string, body: URLSearchParams): RequestInit => ({
  method: "POST",
  headers: {
    Accept: "application/json",
    "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
    "X-BN-CSRF": token,
  },
  body: body.toString(),
});

/** Reads one page of the journal. Read-only, so it carries no token. */
export const fetchHistoryPage = async (
  fetcher: HistoryFetch,
  endpoint: string,
  query: HistoryQuery
): Promise<HistoryOutcome<HistoryResult>> => {
  const params = buildHistoryQuery(query).toString();
  const target = params === "" ? endpoint || HISTORY_ENDPOINT : `${endpoint || HISTORY_ENDPOINT}?${params}`;
  try {
    const resp = await fetcher(target, { headers: { Accept: "application/json" } });
    return await finish(resp, normalizeHistoryResult);
  } catch {
    return unreachable();
  }
};

/**
 * Asks what restoring one entry would do, and takes the one-use receipt that
 * authorizes forcing it.
 *
 * It writes nothing, which is why the page previews before every restore rather
 * than only before a forced one: the cheap question is also the honest one, and a
 * clean restore confirmed against a stale reading is refused rather than applied.
 */
export const previewHistoryRestore = async (
  fetcher: HistoryFetch,
  endpoint: string,
  token: string,
  entryID: string
): Promise<HistoryOutcome<HistoryPreviewResult>> => {
  try {
    const resp = await fetcher(
      endpoint || HISTORY_PREVIEW_ENDPOINT,
      formInit(token, buildHistoryPreviewForm({ token, entryID }))
    );
    return await finish(resp, normalizeHistoryPreview);
  } catch {
    return unreachable();
  }
};

const normalizeUndoResult = (payload: unknown): UndoResult => {
  const record = asRecord(payload) || {};
  return {
    ok: readBoolean(record.ok),
    code: normalizeParamValue(record.code) || undefined,
    message: normalizeParamValue(record.message) || undefined,
    entry_id: normalizeParamValue(record.entry_id) || undefined,
    requires_force: readBoolean(record.requires_force),
  };
};

/**
 * Restores an entry.
 *
 * A 409 carrying requires_force is returned as a failure with code "conflict"
 * rather than as an error: it is the server declining to replace newer work, and
 * the caller's next move is to show that work rather than to report a fault.
 */
export const restoreHistoryEntry = async (
  fetcher: HistoryFetch,
  endpoint: string,
  request: UndoRequest
): Promise<HistoryOutcome<UndoResult>> => {
  try {
    const resp = await fetcher(endpoint || UNDO_ENDPOINT, formInit(request.token, buildUndoForm(request)));
    const payload = await readPayload(resp);
    if (!resp.ok) {
      return failure(resp, payload);
    }
    const result = normalizeUndoResult(payload);
    if (!result.ok) {
      return {
        ok: false,
        code: result.code || "",
        status: resp.status,
        message: result.message || "The restore was refused.",
        conflicts: normalizeUndoConflicts(asRecord(payload)?.conflicts),
        entryID: result.entry_id || normalizeParamValue(asRecord(payload)?.entry_id),
      };
    }
    return { ok: true, result };
  } catch {
    return unreachable();
  }
};
