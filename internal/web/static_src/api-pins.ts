import { buildNoteKeyMutationForm } from "./api-notes";
import type {
  NotePinFetchRequest,
  NotePinReorderRequest,
  NotePinRequest,
} from "./api-types";

export const notePinSetPath = "/api/pins/set";
export const notePinRemovePath = "/api/pins/remove";
export const notePinReorderPath = "/api/pins/reorder";

export const buildNotePinReorderForm = (
  request: NotePinReorderRequest
): URLSearchParams => {
  const body = buildNoteKeyMutationForm(request);
  body.set("before", request.before);
  body.set("after", request.after);
  return body;
};

const notePinFetchRequest = (
  url: string,
  body: URLSearchParams,
  token: string
): NotePinFetchRequest => ({
  url,
  init: {
    method: "POST",
    headers: {
      Accept: "application/json",
      "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
      "X-BN-CSRF": token,
    },
    body: body.toString(),
  },
});

export const buildNotePinSetFetchRequest = (
  request: NotePinRequest
): NotePinFetchRequest =>
  notePinFetchRequest(notePinSetPath, buildNoteKeyMutationForm(request), request.token);

export const buildNotePinRemoveFetchRequest = (
  request: NotePinRequest
): NotePinFetchRequest =>
  notePinFetchRequest(notePinRemovePath, buildNoteKeyMutationForm(request), request.token);

export const buildNotePinReorderFetchRequest = (
  request: NotePinReorderRequest
): NotePinFetchRequest =>
  notePinFetchRequest(notePinReorderPath, buildNotePinReorderForm(request), request.token);

export const readNotePinResponsePayload = async (resp: Response): Promise<unknown> => {
  const contentType = resp.headers?.get("Content-Type") || "";
  try {
    if (contentType.includes("application/json")) {
      return await resp.json();
    }
    return await resp.text();
  } catch {
    return "";
  }
};
