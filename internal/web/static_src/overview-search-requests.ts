import {
  buildBulkApplyForm,
  buildBulkPreviewForm,
  type BulkAction,
} from "./api";
import {
  normalizeBulkFrontmatterOp,
  type BulkActionFields,
  type FetchLike,
} from "./overview-search-model";

export type BulkResponse = {
  resp: Response;
  payload: unknown;
};

export const readBulkResponsePayload = async (resp: Response): Promise<unknown> => {
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

const bulkRequestInit = (csrfToken: string, body: URLSearchParams): RequestInit => ({
  method: "POST",
  headers: {
    Accept: "application/json",
    "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
    "X-BN-CSRF": csrfToken,
  },
  body: body.toString(),
});

export const postBulkPreview = async (
  fetcher: FetchLike,
  csrfToken: string,
  action: BulkAction,
  keys: string[],
  fields: BulkActionFields
): Promise<BulkResponse> => {
  const body = buildBulkPreviewForm({
    action,
    keys,
    token: csrfToken,
    folder: fields.folder,
    property: fields.property,
    op: normalizeBulkFrontmatterOp(fields.op),
    value: fields.value,
  });
  const resp = await fetcher("/api/notes/bulk/preview", bulkRequestInit(csrfToken, body));
  return { resp, payload: await readBulkResponsePayload(resp) };
};

export const postBulkApply = async (
  fetcher: FetchLike,
  csrfToken: string,
  action: BulkAction,
  keys: string[],
  fields: BulkActionFields,
  previewID: string,
  operationID = ""
): Promise<BulkResponse> => {
  const body = buildBulkApplyForm({
    action,
    keys,
    token: csrfToken,
    folder: fields.folder,
    property: fields.property,
    op: normalizeBulkFrontmatterOp(fields.op),
    value: fields.value,
    previewID,
    operationID,
  });
  const resp = await fetcher("/api/notes/bulk", bulkRequestInit(csrfToken, body));
  return { resp, payload: await readBulkResponsePayload(resp) };
};
