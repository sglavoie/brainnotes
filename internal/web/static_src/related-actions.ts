import { asRecord, readBoolean } from "./api-internal";
import { readResponsePayload } from "./daily-brief-http";
import { normalizeParamValue } from "./filters";
import { normalizeIdentity } from "./related-types";
import type {
  RelatedFetch,
  RelatedFeedbackState,
  RelatedItemAction,
  RelatedItemRequest,
  RelatedItemResponse,
} from "./related-types";

const itemActions: readonly RelatedItemAction[] = [
  "view",
  "open_editor",
  "useful",
  "dismissed",
  "later",
  "clear",
];
const feedbackStates: readonly RelatedFeedbackState[] = ["useful", "dismissed", "later"];

export const buildRelatedItemForm = (request: RelatedItemRequest): URLSearchParams => {
  const body = new URLSearchParams();
  body.set("source", request.source);
  body.set("candidate", request.candidate);
  body.set("action", request.action);
  if (request.until) {
    body.set("until", request.until);
  }
  body.set("token", request.token);
  return body;
};

export const buildRelatedItemRequest = (
  request: RelatedItemRequest,
  keepalive = false
): RequestInit => ({
  method: "POST",
  headers: {
    Accept: "application/json",
    "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
    "X-BN-CSRF": request.token,
  },
  body: buildRelatedItemForm(request).toString(),
  ...(keepalive ? { keepalive: true } : {}),
});

export const normalizeRelatedItemResponse = (payload: unknown): RelatedItemResponse | null => {
  const record = asRecord(payload);
  if (!record || !readBoolean(record.ok)) {
    return null;
  }
  const action = normalizeParamValue(record.action).toLowerCase();
  const source = normalizeIdentity(record.source);
  const candidate = normalizeIdentity(record.candidate);
  if (!itemActions.includes(action as RelatedItemAction) || !source || !candidate) {
    return null;
  }
  const stateValue = normalizeParamValue(record.state).toLowerCase();
  const state =
    feedbackStates.includes(stateValue as RelatedFeedbackState) || stateValue === "clear"
      ? (stateValue as RelatedFeedbackState | "clear")
      : undefined;
  return {
    ok: true,
    action: action as RelatedItemAction,
    source,
    candidate,
    state,
    until: normalizeParamValue(record.until) || undefined,
    opened: readBoolean(record.opened),
    recorded:
      typeof record.recorded === "boolean" ? record.recorded : undefined,
    warning: normalizeParamValue(record.warning) || undefined,
  };
};

const itemErrorMessage = (payload: unknown, status: number): string => {
  const record = asRecord(payload);
  return (record && normalizeParamValue(record.message)) || `Related action failed (${status}).`;
};

export const requestRelatedItem = async (
  fetcher: RelatedFetch,
  endpoint: string,
  request: RelatedItemRequest
): Promise<RelatedItemResponse> => {
  const response = await fetcher(endpoint, buildRelatedItemRequest(request));
  const payload = await readResponsePayload(response);
  if (!response.ok) {
    throw new Error(itemErrorMessage(payload, response.status));
  }
  const result = normalizeRelatedItemResponse(payload);
  if (
    !result ||
    result.action !== request.action ||
    result.source.key !== request.source ||
    result.candidate.key !== request.candidate
  ) {
    throw new Error("Related action returned an invalid response.");
  }
  return result;
};

export type RelatedBeacon = (url: string, data?: BodyInit | null) => boolean;

export const trackRelatedView = (
  fetcher: RelatedFetch,
  beacon: RelatedBeacon | undefined,
  endpoint: string,
  request: Omit<RelatedItemRequest, "action">
): void => {
  const viewRequest: RelatedItemRequest = { ...request, action: "view" };
  if (beacon) {
    try {
      const body = new Blob([buildRelatedItemForm(viewRequest).toString()], {
        type: "application/x-www-form-urlencoded; charset=UTF-8",
      });
      if (beacon(endpoint, body)) {
        return;
      }
    } catch {
      // Fall through to a keepalive request. Navigation remains untouched.
    }
  }
  void fetcher(endpoint, buildRelatedItemRequest(viewRequest, true)).catch(() => undefined);
};
