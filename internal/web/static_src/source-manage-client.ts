import { normalizeManagementErrorMessage } from "./api";
import { asRecord, readRecordValue } from "./api-internal";
import { normalizeParamValue } from "./filters";

// Posting one management action from the editor page.
//
// The routes it calls — rename, move, trash, create, capture, frontmatter, pins
// and undo — already existed for the note page (internal/web/manage.go,
// capture.go, pins_api.go, history.go). Nothing new is added on the server; what
// is new is that the editor is now a caller, which is why this is a transport
// with no opinions rather than one function per action: the differences between
// them are which body and what to do afterwards, and both belong with the
// buttons.

export type ManageOutcome = {
  ok: boolean;
  unconfirmed: boolean;
  status: number;
  message: string;
  // key and relPath are the note the operation *ended* at, which is not the one
  // it started at for a rename or a move. The editor navigates to it rather than
  // re-deriving a route from the title somebody typed, because the server is the
  // only thing that knows what filename it settled on.
  key: string;
  relPath: string;
  detail: string;
  payload: unknown;
};

export type ManageRequest = {
  url: string;
  body: URLSearchParams;
  token: string;
  fetch?: typeof fetch;
};

export const postManagement = async (request: ManageRequest): Promise<ManageOutcome> => {
  const response = await (request.fetch ?? fetch)(request.url, {
    method: "POST",
    headers: {
      Accept: "application/json",
      "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
      "X-BN-CSRF": request.token,
    },
    body: request.body.toString(),
  });
  const payload = await readPayload(response);
  return readManageOutcome(response.status, response.ok, payload);
};

export const readManageOutcome = (
  status: number,
  ok: boolean,
  payload: unknown
): ManageOutcome => {
  const record = asRecord(payload) || {};
  if (!ok || record.ok === false) {
    return {
      ok: false,
      unconfirmed: false,
      status,
      message: normalizeManagementErrorMessage(status, payload),
      key: "",
      relPath: "",
      detail: "",
      payload,
    };
  }
  return {
    ok: record.ok === true,
    unconfirmed: record.ok !== true,
    status,
    message: normalizeParamValue(record.message) || "Done.",
    key: normalizeParamValue(record.key),
    relPath: normalizeParamValue(readRecordValue(record, ["rel_path", "RelPath"])),
    detail: normalizeParamValue(record.detail),
    payload,
  };
};

// editorURLForKey is the only place this page builds a destination to navigate
// to, and it builds one rather than reading one off a payload.
//
// The editor route comes from the page's own data attribute — the server
// publishes it, editor_page_test.go pins it — and the key is the one the server
// answered with, encoded. So the destination is same-origin by construction:
// there is no field a response could carry that would turn a rename into a
// redirect somewhere else. noteNavigationDestination in api-notes.ts refuses
// anything that is not a `/n/` route for the same reason, from the other
// direction.
export const editorURLForKey = (editorPath: string, key: string): string =>
  `${editorPath}?key=${encodeURIComponent(key)}`;

const readPayload = async (response: Response): Promise<unknown> => {
  const contentType = response.headers?.get("Content-Type") || "";
  try {
    return contentType.includes("application/json") ? await response.json() : await response.text();
  } catch {
    return null;
  }
};
