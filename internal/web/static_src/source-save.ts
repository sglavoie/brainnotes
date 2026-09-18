import {
  buildSourceCreateForm,
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
  create: string;
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
  create(request: { relPath: string; content: string; saveID: string }): Promise<SourceSaveResponse>;
  search(url: string): Promise<unknown>;
};

// SourceReadError carries the management error code beside the message, because
// the editor answers two of them differently: `not_found` means the note this
// page was opened for is no longer in the vault, which is a recovery choice
// rather than a sentence, while `unreachable` — this module's own code, not the
// server's — means nobody answered for the vault at all and any local draft must
// be kept untouched.
export class SourceReadError extends Error {
  readonly code: string;

  constructor(message: string, code: string) {
    super(message);
    this.name = "SourceReadError";
    this.code = code;
  }
}

// unreachableCode marks an answer that did not come from this API.
//
// The case that forces it is an Access-style login redirect: the session cookie
// expires, /api/source answers 302 to an identity provider, `fetch` follows it,
// and the sign-in page comes back **200 OK**. `response.ok` is true, the body is
// HTML, and a normalizer that reads fields off it yields empty content with an
// empty revision — an editor that would then show a blank note over a note full
// of text, with a local draft dropped as "already in the vault". So a foreign
// answer is a failure here, and a loud one.
export const unreachableCode = "unreachable";

const signInMessage =
  "The vault did not answer — the reply came from somewhere else, which usually means a sign-in has expired. Nothing was lost; sign in again and reload this page.";

// foreignSaveMessage says the one thing a redirected save leaves true: nobody
// knows. The bytes may have reached the vault before the session lapsed, or the
// request may never have arrived at all, so this must not read as a refusal.
const foreignSaveMessage =
  "The vault did not answer — the reply came from somewhere else, which usually means a sign-in has expired, so whether the note was written is unknown. Sign in again and press Save to retry; the same submission cannot be written twice.";

// foreignAnswer decides whether a response came from this API at all.
//
// `redirected` catches the login hop even when the sign-in page is valid JSON,
// and a payload that is not a JSON object catches the rest: every arm of every
// route on this surface answers with an object, so an HTML page, an empty body
// and a truncated one all land here.
const foreignAnswer = (response: Response, payload: unknown): boolean =>
  response.redirected || payload === null || typeof payload !== "object";

type SourcePoster = (
  url: string,
  body: URLSearchParams
) => Promise<{ response: Response; payload: unknown }>;

const readSource = async (
  fetcher: typeof fetch,
  endpoint: string,
  key: string
): Promise<SourceDocument> => {
  const url = `${endpoint}?key=${encodeURIComponent(key)}`;
  const response = await fetcher(url, { headers: { Accept: "application/json" } });
  const payload = await readJSON(response);
  // The foreign check comes first, ahead of `response.ok`, because the answer
  // this most needs to catch is a successful one: a sign-in page served 200 in
  // place of the note.
  if (foreignAnswer(response, payload)) {
    throw new SourceReadError(signInMessage, unreachableCode);
  }
  if (!response.ok) {
    throw new SourceReadError(
      messageFrom(payload, `The note's source could not be loaded (${response.status}).`),
      codeFrom(payload)
    );
  }
  return normalizeSourceDocument(payload);
};

const previewSource = async (
  post: SourcePoster,
  endpoint: string,
  content: string,
  token: string
): Promise<string> => {
  const { response, payload } = await post(endpoint, buildSourcePreviewForm({ content, token }));
  if (foreignAnswer(response, payload)) {
    // The preview pane is the one place a foreign answer is merely cosmetic,
    // but blanking it silently would read as "this note renders to nothing" —
    // which is exactly what an expired session should not be allowed to look
    // like.
    throw new Error(signInMessage);
  }
  const html = normalizeSourcePreviewHTML(payload);
  if (html === "") {
    // An empty render is legitimate for an empty note, so this only reports as
    // an error when the server also said it failed.
    const message = messageFrom(payload, "");
    if (message !== "") {
      throw new Error(message);
    }
  }
  return html;
};

export const createSourceClient = (options: SourceClientOptions): SourceClient => {
  const fetcher = options.fetch || fetch;

  // post hands back the response beside the parsed body, because whether an
  // answer is this API's at all is a fact about the response — a followed
  // redirect — and not only about the bytes it carried.
  const post = async (
    url: string,
    body: URLSearchParams
  ): Promise<{ response: Response; payload: unknown }> => {
    const response = await fetcher(url, {
      method: "POST",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
        "X-BN-CSRF": options.token,
      },
      body: body.toString(),
    });
    return { response, payload: await readJSON(response) };
  };

  // postSave is the body /api/source/save and /api/source/create share. A
  // foreign answer becomes a refusal with no code, which is how the rest of this
  // module already spells "nobody said whether the bytes landed": the attempt's
  // save_id survives, and pressing Save again replays it rather than writing a
  // second time.
  const postSave = async (url: string, body: URLSearchParams): Promise<SourceSaveResponse> => {
    const { response, payload } = await post(url, body);
    if (foreignAnswer(response, payload)) {
      return { ok: false, code: "", message: foreignSaveMessage, revision: "", content: "", changed: false };
    }
    return normalizeSourceSaveResponse(payload);
  };

  return {
    read: (): Promise<SourceDocument> => readSource(fetcher, options.endpoints.read, options.key),

    preview: (content: string): Promise<string> =>
      previewSource(post, options.endpoints.preview, content, options.token),

    // save never throws on a refusal. Every arm of the envelope is a fact the
    // editor has to act on — a stale revision offers a merge, a snapshot
    // failure means the bytes landed — and an exception would flatten all of
    // them into "something went wrong".
    async save(request): Promise<SourceSaveResponse> {
      return postSave(
        options.endpoints.save,
        buildSourceSaveForm({
          key: options.key,
          content: request.content,
          revision: request.revision,
          save_id: request.saveID,
          token: options.token,
        })
      );
    },

    // create writes a note at a path rather than over a key, and it is here for
    // exactly one caller: a recovered draft whose note is no longer in the
    // vault. It carries no revision because there is nothing to have been
    // planned against — the path either is free, or the server refuses with the
    // bytes that are in the way.
    async create(request): Promise<SourceSaveResponse> {
      return postSave(
        options.endpoints.create,
        buildSourceCreateForm({
          rel_path: request.relPath,
          content: request.content,
          save_id: request.saveID,
          token: options.token,
        })
      );
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
export const newSaveID = (): string => randomID("save");

// randomID is the minter behind every identifier this editor makes up: a save
// attempt's idempotency key, and a tab's own draft id. They are the same problem
// — a name nothing else will pick — so they are the same three lines rather than
// two spellings of them that drift.
export const randomID = (prefix: string): string => {
  const cryptoAPI = globalThis.crypto;
  if (cryptoAPI && typeof cryptoAPI.randomUUID === "function") {
    return cryptoAPI.randomUUID();
  }
  // A fallback for a context without the Web Crypto API. Uniqueness here only
  // has to hold within one browser profile, since every id minted here is scoped
  // to one note and one tab.
  return `${prefix}-${Math.random().toString(36).slice(2)}-${Math.random().toString(36).slice(2)}`;
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
  // "missing" is the note this editor was opened for no longer being in the
  // vault — renamed, moved or deleted while the tab stayed open. It is separated
  // from "failed" because it is the one refusal with a remedy the editor can
  // offer: the text on screen has nowhere to go back to, so it is offered as a
  // new note at the path this page was opened for, and never written by itself.
  | { kind: "missing"; message: string }
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
  return classifyFailedSave(response, baseRevision);
};

// classifyFailedSave reads the refusals, in the order their remedies differ: the
// bytes landed anyway, the note is gone, a precondition that is not the note's
// failed, and then everything else.
const classifyFailedSave = (response: SourceSaveFailure, baseRevision: string): SaveOutcome => {
  if (response.changed && response.revision !== "") {
    return {
      kind: "landed",
      revision: response.revision,
      message: response.message || "The note was written but could not be snapshotted.",
    };
  }
  if (response.code === notFoundCode) {
    return {
      kind: "missing",
      message:
        response.message ||
        "This note is no longer in the vault, so there was nothing to save over. Your text is still here.",
    };
  }
  if (isUnmovedConflict(response, baseRevision)) {
    return {
      kind: "failed",
      answered: true,
      message:
        "Something else in the vault changed while the note was being saved, so nothing was written. Your text is unchanged; press Save to try again.",
    };
  }
  return classifyRefusal(response);
};

// isUnmovedConflict names a refusal whose revision did not move: a precondition
// that failed somewhere other than the note, which in practice is the
// configuration pin.
//
// It gets a sentence of its own because the server's is untrue here. The message
// reads "<note> changed since it was loaded" — newSourceRevisionError writes one
// for every precondition and classifySourceSaveTxnError routes them all through
// it — and passing that on would tell somebody their note moved while showing
// them no other version and no way to merge. What is true is the part that
// matters: nothing was written, and trying again is the remedy.
const isUnmovedConflict = (response: SourceSaveResponse, baseRevision: string): boolean =>
  !response.ok &&
  response.code === "conflict" &&
  baseRevision !== "" &&
  response.revision === baseRevision;

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

// notFoundCode is noteops.CodeNotFound on the wire (internal/noteops/errors.go).
// Both doors report it: the route resolver when the key has left the snapshot,
// and the save transaction itself when the file is gone from under an expectation
// the snapshot still believes.
export const notFoundCode = "not_found";

const codeFrom = (payload: unknown): string => {
  if (payload && typeof payload === "object") {
    const code = (payload as Record<string, unknown>).code;
    if (typeof code === "string") {
      return code.trim();
    }
  }
  return "";
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
