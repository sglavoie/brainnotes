import { normalizeManagementErrorMessage } from "./api";

// The attachment staging transport: one file up, one cancel back.
//
// It is the only module in the editor that does not use `fetch`, and the reason
// is progress. A 25 MB upload over a home connection is tens of seconds of a
// person watching nothing happen, and `fetch` has no upload-progress event in
// any shipping browser; XMLHttpRequest does. The request object is injected
// rather than constructed, so every one of the four ways an upload can end —
// staged, refused, aborted, no answer at all — is exercised under jsdom without
// a network.
//
// /api/attachments/upload takes the file as the raw body with `owner` and `name`
// in the query string (internal/web/attachments_api.go), so the CSRF token has
// to travel in the X-BN-CSRF header: the server cannot call ParseForm on a body
// it is streaming to disk.

export type StagedUpload = {
  uploadID: string;
  filename: string;
  // relPath is the destination staging *reserved*, not a guess. It is what the
  // link inserted into the note names, which is why the reservation happens at
  // upload time rather than at save time — see attachmentUploadResponse.
  relPath: string;
  size: number;
  sha256: string;
};

export type UploadOutcome =
  | { kind: "staged"; staged: StagedUpload }
  // "canceled" is this page's own abort, and is not an error: the person asked.
  | { kind: "canceled" }
  | { kind: "failed"; message: string };

export type UploadProgress = { loaded: number; total: number };

// UploadRequestLike is the slice of XMLHttpRequest used here. A real one
// satisfies it structurally, which is the point: the production path assigns
// `new XMLHttpRequest()` and nothing adapts it.
export type UploadRequestLike = {
  open(method: string, url: string): void;
  setRequestHeader(name: string, value: string): void;
  send(body: Blob): void;
  abort(): void;
  upload: { onprogress: ((event: UploadProgressEventLike) => void) | null };
  onload: (() => void) | null;
  onerror: (() => void) | null;
  onabort: (() => void) | null;
  status: number;
  responseText: string;
};

export type UploadProgressEventLike = {
  lengthComputable: boolean;
  loaded: number;
  total: number;
};

export type AttachmentUploadOptions = {
  endpoint: string;
  owner: string;
  token: string;
  filename: string;
  file: Blob;
  onProgress?: (progress: UploadProgress) => void;
  newRequest?: () => UploadRequestLike;
};

export type UploadHandle = {
  outcome: Promise<UploadOutcome>;
  cancel(): void;
};

// noAnswerMessage is the sentence for a request that produced no response at
// all. It says the one thing that is true — the file did not reach the vault —
// because staging is a single request with no ledger behind it: unlike a save,
// an upload nobody answered for has written nothing this page must account for.
const noAnswerMessage =
  "The file could not be sent to the vault. Nothing was uploaded; press Retry to try again.";

export const uploadAttachment = (options: AttachmentUploadOptions): UploadHandle => {
  const request = (options.newRequest ?? (() => new XMLHttpRequest() as UploadRequestLike))();
  let canceled = false;

  const outcome = new Promise<UploadOutcome>((resolve) => {
    request.upload.onprogress = (event): void => {
      if (event.lengthComputable && options.onProgress) {
        options.onProgress({ loaded: event.loaded, total: event.total });
      }
    };
    request.onload = (): void => resolve(readUploadResponse(request));
    request.onerror = (): void => resolve({ kind: "failed", message: noAnswerMessage });
    // An abort resolves rather than rejects, and reports the cancellation
    // rather than a failure: the caller asked for it, and a rejected promise
    // here would put "upload failed" on screen beside a button the person just
    // pressed to stop it.
    request.onabort = (): void => resolve({ kind: "canceled" });
  });

  request.open("POST", uploadURL(options.endpoint, options.owner, options.filename));
  request.setRequestHeader("X-BN-CSRF", options.token);
  request.setRequestHeader("Accept", "application/json");
  request.setRequestHeader("Content-Type", "application/octet-stream");
  request.send(options.file);

  return {
    outcome,
    cancel(): void {
      if (canceled) {
        return;
      }
      canceled = true;
      request.abort();
    },
  };
};

export const uploadURL = (endpoint: string, owner: string, filename: string): string =>
  `${endpoint}?owner=${encodeURIComponent(owner)}&name=${encodeURIComponent(filename)}`;

// readUploadResponse reads the one envelope this route answers with.
//
// A body that is not this envelope is reported as a failure rather than parsed
// optimistically, for the reason source-save.ts spells out at length: a sign-in
// page served 200 in place of the answer would otherwise stage an upload id of
// "" and put an empty link in somebody's note.
const readUploadResponse = (request: UploadRequestLike): UploadOutcome => {
  const payload = parseJSON(request.responseText);
  const record = payload && typeof payload === "object" ? (payload as Record<string, unknown>) : null;
  if (!record) {
    return { kind: "failed", message: noAnswerMessage };
  }
  if (request.status < 200 || request.status >= 300 || record.ok !== true) {
    return { kind: "failed", message: normalizeManagementErrorMessage(request.status, payload) };
  }
  const uploadID = text(record.upload_id);
  const relPath = text(record.rel_path);
  if (uploadID === "" || relPath === "") {
    return { kind: "failed", message: noAnswerMessage };
  }
  return {
    kind: "staged",
    staged: {
      uploadID,
      filename: text(record.filename),
      relPath,
      size: typeof record.size === "number" ? record.size : 0,
      sha256: text(record.sha256),
    },
  };
};

export type CancelUploadOptions = {
  endpoint: string;
  owner: string;
  uploadID: string;
  token: string;
  fetch?: typeof fetch;
};

// cancelStagedUpload tells the server to drop bytes this page will not commit.
//
// It reports nothing and refuses nothing. A cancel is a courtesy — the server's
// own sweep collects abandoned staging either way — so a failure here costs disk
// on a timer and never a sentence on a page where the person has already moved
// on. The promise is still returned so a test can wait for it.
export const cancelStagedUpload = async (options: CancelUploadOptions): Promise<void> => {
  const body = new URLSearchParams();
  body.set("owner", options.owner);
  body.set("upload_id", options.uploadID);
  body.set("token", options.token);
  try {
    await (options.fetch ?? fetch)(options.endpoint, {
      method: "POST",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
        "X-BN-CSRF": options.token,
      },
      body: body.toString(),
    });
  } catch {
    // Deliberately silent; see above.
  }
};

const parseJSON = (raw: string): unknown => {
  try {
    return JSON.parse(raw) as unknown;
  } catch {
    return null;
  }
};

const text = (value: unknown): string => (typeof value === "string" ? value.trim() : "");
