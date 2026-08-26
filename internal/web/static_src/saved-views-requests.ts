import {
  SAVED_VIEWS_APPLY_ENDPOINT,
  SAVED_VIEWS_ENDPOINT,
  SAVED_VIEWS_PREVIEW_ENDPOINT,
  buildSavedViewsRequestInit,
  normalizeManagementErrorMessage,
  normalizeSavedViewsResponse,
  type SavedViewApplyRequest,
  type SavedViewMutationRequest,
  type SavedViewsRequestBody,
  type SavedViewsResult,
} from "./api";
import type { FetchLike } from "./saved-views-model";

export type SavedViewsOutcome =
  | { ok: true; result: SavedViewsResult }
  | { ok: false; message: string };

const readPayload = async (resp: Response): Promise<unknown> => {
  try {
    return await resp.json();
  } catch {
    return null;
  }
};

const finish = async (resp: Response): Promise<SavedViewsOutcome> => {
  const payload = await readPayload(resp);
  if (!resp.ok) {
    return { ok: false, message: normalizeManagementErrorMessage(resp.status, payload) };
  }
  const result = normalizeSavedViewsResponse(payload);
  if (!result.ok) {
    return { ok: false, message: result.message || "The change was refused." };
  }
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
    return await finish(resp);
  } catch {
    return { ok: false, message: "The server did not answer." };
  }
};

const postSavedViews = async (
  fetcher: FetchLike,
  endpoint: string,
  token: string,
  body: SavedViewsRequestBody
): Promise<SavedViewsOutcome> => {
  try {
    const resp = await fetcher(endpoint, buildSavedViewsRequestInit(token, body));
    return await finish(resp);
  } catch {
    return { ok: false, message: "The server did not answer." };
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
  previewID: string
): Promise<SavedViewsOutcome> =>
  postSavedViews(fetcher, SAVED_VIEWS_APPLY_ENDPOINT, token, {
    preview_id: previewID,
  } satisfies SavedViewApplyRequest);
