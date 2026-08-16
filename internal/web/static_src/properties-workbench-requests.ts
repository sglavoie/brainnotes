import {
  PROPERTY_APPLY_ENDPOINT,
  PROPERTY_PREVIEW_ENDPOINT,
  PROPERTY_SCHEMA_APPLY_ENDPOINT,
  PROPERTY_SCHEMA_PREVIEW_ENDPOINT,
  PROPERTY_VALUE_ENDPOINT,
  buildPropertyRequestInit,
  buildUndoForm,
  normalizeManagementErrorMessage,
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
  | { ok: false; message: string };

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
  try {
    const resp = await fetcher(endpoint, buildPropertyRequestInit(token, body));
    const payload = await readPayload(resp);
    if (!resp.ok) {
      return { ok: false, message: normalizeManagementErrorMessage(resp.status, payload) };
    }
    const result = normalizePropertyMutationResponse(payload);
    if (!result.ok) {
      return { ok: false, message: result.message || "The change was refused." };
    }
    return { ok: true, result };
  } catch {
    return { ok: false, message: "The server did not answer." };
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
  previewID: string
): Promise<MutationOutcome> =>
  postProperty(fetcher, PROPERTY_APPLY_ENDPOINT, token, {
    preview_id: previewID,
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
  previewID: string
): Promise<MutationOutcome> =>
  postProperty(fetcher, PROPERTY_SCHEMA_APPLY_ENDPOINT, token, {
    preview_id: previewID,
  } satisfies PropertyApplyRequest);

export type UndoOutcome = { ok: boolean; message: string };

// undoJournalEntry reverses one journal entry through the same unified history
// endpoint the history panel uses, so an undo from the toast and an undo from
// the panel are the same operation.
export const undoJournalEntry = async (
  fetcher: FetchLike,
  token: string,
  entryID: string
): Promise<UndoOutcome> => {
  try {
    const resp = await fetcher("/api/undo", {
      method: "POST",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
        "X-BN-CSRF": token,
      },
      body: buildUndoForm({ token, entryID }).toString(),
    });
    const payload = await readPayload(resp);
    if (!resp.ok) {
      return { ok: false, message: normalizeManagementErrorMessage(resp.status, payload) };
    }
    return { ok: true, message: "Change undone." };
  } catch {
    return { ok: false, message: "Undo failed." };
  }
};
