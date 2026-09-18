import {
  buildSourcePreviewForm,
  buildSourceSaveForm,
  normalizeSourceDocument,
  normalizeSourcePreviewHTML,
  normalizeSourceSaveResponse,
  sourceSaveConflict,
  type SourceDocument,
  type SourceSaveFailure,
  type SourceSaveResponse,
} from "./api";

// The transport half of the editor: reading a note's source, rendering a
// preview, and saving.
//
// It is a module of functions over an injected `fetch` rather than a class,
// because every one of them is a request/response pair and the tests want to
// answer them by hand.

export type FetchLike = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;

export type SourceEndpoints = {
  read: string;
  preview: string;
  save: string;
  search: string;
};

export type SourceClientOptions = {
  endpoints: SourceEndpoints;
  key: string;
  token: string;
  fetch?: FetchLike;
};

export type SourceClient = {
  read(): Promise<SourceDocument>;
  preview(content: string): Promise<string>;
  save(request: { content: string; revision: string; saveID: string }): Promise<SourceSaveResponse>;
  search(url: string): Promise<unknown>;
};

export const createSourceClient = (options: SourceClientOptions): SourceClient => {
  const fetcher = options.fetch || fetch;

  const post = async (url: string, body: URLSearchParams): Promise<unknown> => {
    const response = await fetcher(url, {
      method: "POST",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
        "X-BN-CSRF": options.token,
      },
      body: body.toString(),
    });
    return readJSON(response);
  };

  return {
    async read(): Promise<SourceDocument> {
      const url = `${options.endpoints.read}?key=${encodeURIComponent(options.key)}`;
      const response = await fetcher(url, { headers: { Accept: "application/json" } });
      const payload = await readJSON(response);
      if (!response.ok) {
        throw new Error(messageFrom(payload, `The note's source could not be loaded (${response.status}).`));
      }
      return normalizeSourceDocument(payload);
    },

    async preview(content: string): Promise<string> {
      const payload = await post(
        options.endpoints.preview,
        buildSourcePreviewForm({ content, token: options.token })
      );
      const html = normalizeSourcePreviewHTML(payload);
      if (html === "") {
        // An empty render is legitimate for an empty note, so this only
        // reports as an error when the server also said it failed.
        const message = messageFrom(payload, "");
        if (message !== "") {
          throw new Error(message);
        }
      }
      return html;
    },

    // save never throws on a refusal. Every arm of the envelope is a fact the
    // editor has to act on — a stale revision offers a merge, a snapshot
    // failure means the bytes landed — and an exception would flatten all of
    // them into "something went wrong".
    async save(request): Promise<SourceSaveResponse> {
      const payload = await post(
        options.endpoints.save,
        buildSourceSaveForm({
          key: options.key,
          content: request.content,
          revision: request.revision,
          save_id: request.saveID,
          token: options.token,
        })
      );
      return normalizeSourceSaveResponse(payload);
    },

    async search(url: string): Promise<unknown> {
      const response = await fetcher(url, { headers: { Accept: "application/json" } });
      if (!response.ok) {
        return [];
      }
      return readJSON(response);
    },
  };
};

// newSaveID mints the idempotency key for one save attempt.
//
// The server keeps a per-save_id ledger, so resubmitting the same id replays
// the recorded outcome instead of writing twice. That makes the id's *lifetime*
// the load-bearing part: it belongs to an attempt, not to a request, so a retry
// after a failure whose outcome is unknown must carry the same one. The editor
// holds it in `pendingSaveID` and clears it only once the server has answered.
export const newSaveID = (): string => {
  const cryptoAPI = globalThis.crypto;
  if (cryptoAPI && typeof cryptoAPI.randomUUID === "function") {
    return cryptoAPI.randomUUID();
  }
  // A fallback for a context without the Web Crypto API. Uniqueness here only
  // has to hold within one editor session, since the id is scoped to one note
  // and one browser tab.
  return `save-${Math.random().toString(36).slice(2)}-${Math.random().toString(36).slice(2)}`;
};

// SaveOutcome is what the editor acts on: one of four cases, each needing a
// different next step rather than a different sentence.
export type SaveOutcome =
  | { kind: "saved"; revision: string; message: string }
  | { kind: "conflict"; revision: string; content: string; message: string }
  // "landed" is the snapshot failure: the bytes are on disk, the content
  // history does not cover them, and further writes are blocked. The editor
  // must re-pin to the new revision — resubmitting against the old one would
  // be a save planned against a revision its own write replaced.
  | { kind: "landed"; revision: string; message: string }
  // "failed" is a refusal there is nothing to do about but report it.
  // `answered` says whether *this API* refused: an envelope carrying a code is
  // the server declining, and nothing was written. A body that is not this
  // envelope at all — a proxy's own 502 page, a truncated response — leaves
  // whether the bytes landed unknown, which is precisely the case the save_id
  // ledger exists for, so the attempt's id must survive it.
  | { kind: "failed"; message: string; answered: boolean };

// classifySaveResponse needs the revision the save was planned against, because
// one of the four outcomes is not a property of the response alone: a refusal
// only offers a merge when the version it carries is not the one already in the
// editor. See sourceSaveConflict.
export const classifySaveResponse = (
  response: SourceSaveResponse,
  baseRevision: string
): SaveOutcome => {
  if (response.ok) {
    return { kind: "saved", revision: response.revision, message: response.message || "Saved." };
  }
  const conflict = sourceSaveConflict(response, baseRevision);
  if (conflict) {
    return {
      kind: "conflict",
      revision: conflict.revision,
      content: conflict.content,
      message: response.message || "The note changed since it was loaded, so nothing was written.",
    };
  }
  if (response.changed && response.revision !== "") {
    return {
      kind: "landed",
      revision: response.revision,
      message: response.message || "The note was written but could not be snapshotted.",
    };
  }
  return classifyRefusal(response);
};

// classifyRefusal tells a refusal this API sent from an answer that was never
// this API's. Every field of the envelope is absent from a proxy's error page
// and a truncated body alike, so `code` is what carries the distinction: the
// server never refuses without naming why.
const classifyRefusal = (response: SourceSaveFailure): SaveOutcome => {
  if (response.code === "") {
    return {
      kind: "failed",
      answered: false,
      message:
        response.message ||
        "The server's answer could not be read, so whether the note was written is unknown. Press Save to retry; the same submission cannot be written twice.",
    };
  }
  return { kind: "failed", answered: true, message: response.message || "The note could not be saved." };
};

const readJSON = async (response: Response): Promise<unknown> => {
  try {
    return await response.json();
  } catch {
    return null;
  }
};

const messageFrom = (payload: unknown, fallback: string): string => {
  if (payload && typeof payload === "object") {
    const message = (payload as Record<string, unknown>).message;
    if (typeof message === "string" && message.trim() !== "") {
      return message.trim();
    }
  }
  return fallback;
};
