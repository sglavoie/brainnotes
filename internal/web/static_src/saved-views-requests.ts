import { announceConfirmedWrite } from "./write-notice";
import { responseFailure, unansweredRequest, type RequestPurpose } from "./request-outcome";
import {
  SAVED_VIEWS_APPLY_ENDPOINT,
  SAVED_VIEWS_ENDPOINT,
  SAVED_VIEWS_PREVIEW_ENDPOINT,
  buildSavedViewsRequestInit,
  normalizeSavedViewsResponse,
  type SavedViewApplyRequest,
  type SavedViewMutationRequest,
  type SavedViewsRequestBody,
  type SavedViewsResult,
} from "./api";
import type { FetchLike } from "./saved-views-model";

export type SavedViewsOutcome =
  | { ok: true; result: SavedViewsResult }
  | { ok: false; message: string; uncertain?: boolean };

const readPayload = async (resp: Response): Promise<unknown> => {
  try {
    return await resp.json();
  } catch {
    return null;
  }
};

const finish = async (resp: Response, purpose: RequestPurpose): Promise<SavedViewsOutcome> => {
  const payload = await readPayload(resp);
  const failure = responseFailure(resp, payload, purpose);
  if (failure) return failure;
  const result = normalizeSavedViewsResponse(payload);
  if (purpose === "write") announceConfirmedWrite();
  return { ok: true, result };
};

/**
 * Lists the saved views. The route is read-only and takes no CSRF token, per
 * the read-only route class.
 */
export const fetchSavedViews = async (fetcher: FetchLike): Promise<SavedViewsOutcome> => {
  try {
    const resp = await fetcher(SAVED_VIEWS_ENDPOINT, {
      headers: { Accept: "application/json" },
    });
    return await finish(resp, "read");
  } catch {
    return unansweredRequest("read");
  }
};

const postSavedViews = async (
  fetcher: FetchLike,
  endpoint: string,
  token: string,
  body: SavedViewsRequestBody
): Promise<SavedViewsOutcome> => {
  const purpose = endpoint === SAVED_VIEWS_APPLY_ENDPOINT ? "write" : "preview";
  try {
    const resp = await fetcher(endpoint, buildSavedViewsRequestInit(token, body));
    return await finish(resp, purpose);
  } catch {
    return unansweredRequest(purpose);
  }
};

export const previewSavedView = (
  fetcher: FetchLike,
  token: string,
  body: SavedViewMutationRequest
): Promise<SavedViewsOutcome> =>
  postSavedViews(fetcher, SAVED_VIEWS_PREVIEW_ENDPOINT, token, body);

export const applySavedView = (
  fetcher: FetchLike,
  token: string,
  previewID: string,
  operationID = ""
): Promise<SavedViewsOutcome> =>
  postSavedViews(fetcher, SAVED_VIEWS_APPLY_ENDPOINT, token, {
    preview_id: previewID,
    ...(operationID ? { operation_id: operationID } : {}),
  } satisfies SavedViewApplyRequest);
