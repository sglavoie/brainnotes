import { announceConfirmedWrite } from "./write-notice";
import { responseFailure, unansweredRequest, type RequestFailure } from "./request-outcome";
import {
  PROPERTY_APPLY_ENDPOINT,
  PROPERTY_PREVIEW_ENDPOINT,
  PROPERTY_SCHEMA_APPLY_ENDPOINT,
  PROPERTY_SCHEMA_PREVIEW_ENDPOINT,
  PROPERTY_VALUE_ENDPOINT,
  buildPropertyRequestInit,
  buildUndoForm,
  normalizePropertyMutationResponse,
  type PropertyApplyRequest,
  type PropertyMutationRequest,
  type PropertyMutationResult,
  type PropertyRequestBody,
  type PropertySchemaRequest,
  type PropertyValueEditRequest,
} from "./api";
import type { FetchLike } from "./properties-model";

export type MutationOutcome =
  | { ok: true; result: PropertyMutationResult }
  | RequestFailure;

const readPayload = async (resp: Response): Promise<unknown> => {
  try {
    return await resp.json();
  } catch {
    return null;
  }
};

const postProperty = async (
  fetcher: FetchLike,
  endpoint: string,
  token: string,
  body: PropertyRequestBody
): Promise<MutationOutcome> => {
  const purpose = endpoint.endsWith("/preview") ? "preview" : "write";
  try {
    const resp = await fetcher(endpoint, buildPropertyRequestInit(token, body));
    const payload = await readPayload(resp);
    const failure = responseFailure(resp, payload, purpose);
    if (failure) return failure;
    const result = normalizePropertyMutationResponse(payload);
    if (purpose === "write") announceConfirmedWrite();
    return { ok: true, result };
  } catch {
    return unansweredRequest(purpose);
  }
};

export const previewPropertyMutation = (
  fetcher: FetchLike,
  token: string,
  body: PropertyMutationRequest
): Promise<MutationOutcome> =>
  postProperty(fetcher, PROPERTY_PREVIEW_ENDPOINT, token, body);

export const applyPropertyMutation = (
  fetcher: FetchLike,
  token: string,
  previewID: string,
  operationID = ""
): Promise<MutationOutcome> =>
  postProperty(fetcher, PROPERTY_APPLY_ENDPOINT, token, {
    preview_id: previewID,
    operation_id: operationID,
  } satisfies PropertyApplyRequest);

export const savePropertyValue = (
  fetcher: FetchLike,
  token: string,
  body: PropertyValueEditRequest
): Promise<MutationOutcome> => postProperty(fetcher, PROPERTY_VALUE_ENDPOINT, token, body);

export const previewPropertySchema = (
  fetcher: FetchLike,
  token: string,
  body: PropertySchemaRequest
): Promise<MutationOutcome> =>
  postProperty(fetcher, PROPERTY_SCHEMA_PREVIEW_ENDPOINT, token, body);

export const applyPropertySchema = (
  fetcher: FetchLike,
  token: string,
  previewID: string,
  operationID = ""
): Promise<MutationOutcome> =>
  postProperty(fetcher, PROPERTY_SCHEMA_APPLY_ENDPOINT, token, {
    preview_id: previewID,
    operation_id: operationID,
  } satisfies PropertyApplyRequest);

export type UndoOutcome = { ok: boolean; message: string; uncertain?: boolean };

// undoJournalEntry reverses one journal entry through the same unified history
// endpoint the history panel uses, so an undo from the toast and an undo from
// the panel are the same operation.
export const undoJournalEntry = async (
  fetcher: FetchLike,
  token: string,
  entryID: string,
  operationID = ""
): Promise<UndoOutcome> => {
  try {
    const resp = await fetcher("/api/undo", {
      method: "POST",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
        "X-BN-CSRF": token,
      },
      body: buildUndoForm({ token, entryID, operationID }).toString(),
    });
    const payload = await readPayload(resp);
    const failure = responseFailure(resp, payload, "write");
    if (failure) return failure;
    announceConfirmedWrite();
    return { ok: true, message: "Change undone." };
  } catch {
    return unansweredRequest("write");
  }
};
