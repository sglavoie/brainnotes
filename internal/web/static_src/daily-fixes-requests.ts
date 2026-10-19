import {
  buildCockpitItemForm,
  buildFixApplyForm,
  buildFixPreviewForm,
  type CockpitItemAction,
  type CockpitSnooze,
  type FixAction,
} from "./api";
import type { FixesFetchRequest } from "./daily-fixes-model";

export const jsonPostRequest = (
  url: string,
  csrfToken: string,
  body: string
): FixesFetchRequest => ({
  url,
  init: {
    method: "POST",
    headers: {
      Accept: "application/json",
      "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
      "X-BN-CSRF": csrfToken,
    },
    body,
  },
});

export const buildFixesItemFetchRequest = (
  endpoint: string,
  csrfToken: string,
  id: string,
  action: CockpitItemAction,
  snooze?: CockpitSnooze
): FixesFetchRequest => {
  const body = buildCockpitItemForm({ id, action, token: csrfToken, snooze });
  return jsonPostRequest(endpoint, csrfToken, body.toString());
};

export const buildFixesPreviewFetchRequest = (
  endpoint: string,
  csrfToken: string,
  id: string,
  action: FixAction
): FixesFetchRequest => {
  const body = buildFixPreviewForm({ id, action, token: csrfToken });
  return jsonPostRequest(endpoint, csrfToken, body.toString());
};

export const buildFixesApplyFetchRequest = (
  endpoint: string,
  csrfToken: string,
  id: string,
  action: FixAction,
  previewID: string,
  operationID = ""
): FixesFetchRequest => {
  const body = buildFixApplyForm({ id, action, token: csrfToken, previewID });
  if (operationID) body.set("operation_id", operationID);
  return jsonPostRequest(endpoint, csrfToken, body.toString());
};

export const readResponsePayload = async (resp: Response): Promise<unknown> => {
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
