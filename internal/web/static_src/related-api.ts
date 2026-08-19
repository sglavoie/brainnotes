import { asRecord, readBoolean } from "./api-internal";
import { normalizeParamValue } from "./filters";
import { readResponsePayload } from "./daily-brief-http";
import { normalizeIdentity } from "./related-types";
import type {
  RelatedFeedback,
  RelatedFeedbackState,
  RelatedFetch,
  RelatedMode,
  RelatedReason,
  RelatedReasonKind,
  RelatedResponse,
  RelatedResult,
} from "./related-types";

const relatedModes: readonly RelatedMode[] = ["balanced", "rediscover", "connect"];
const reasonKinds: readonly RelatedReasonKind[] = ["text", "graph", "metadata", "recall"];
const feedbackStates: readonly RelatedFeedbackState[] = ["useful", "dismissed", "later"];

const normalizeRelatedMode = (value: unknown): RelatedMode | null => {
  const mode = normalizeParamValue(value).toLowerCase();
  return relatedModes.includes(mode as RelatedMode) ? (mode as RelatedMode) : null;
};

const normalizeReason = (value: unknown): RelatedReason | null => {
  const record = asRecord(value);
  if (!record) {
    return null;
  }
  const kind = normalizeParamValue(record.kind).toLowerCase();
  const detail = normalizeParamValue(record.detail);
  if (!reasonKinds.includes(kind as RelatedReasonKind) || detail === "") {
    return null;
  }
  return { kind: kind as RelatedReasonKind, detail };
};

const normalizeRelationship = (
  value: unknown
): RelatedResult["relationship"] => {
  switch (normalizeParamValue(value).toLowerCase()) {
    case "direct":
      return "direct";
    case "shared-neighbor":
      return "shared-neighbor";
    case "two-hop":
      return "two-hop";
    default:
      return "";
  }
};

const normalizeResult = (value: unknown): RelatedResult | null => {
  const record = asRecord(value);
  if (!record) {
    return null;
  }
  const candidate = normalizeIdentity(record.candidate);
  if (!candidate) {
    return null;
  }
  const bridge = normalizeIdentity(record.bridge);
  const reasons = Array.isArray(record.reasons)
    ? record.reasons.flatMap((reason) => {
        const normalized = normalizeReason(reason);
        return normalized ? [normalized] : [];
      }).slice(0, 3)
    : [];
  return {
    candidate,
    excerpt: normalizeParamValue(record.excerpt),
    relationship: normalizeRelationship(record.relationship),
    bridge: bridge || undefined,
    reasons,
    useful: readBoolean(record.useful),
  };
};

const normalizeFeedback = (value: unknown): RelatedFeedback | null => {
  const record = asRecord(value);
  if (!record) {
    return null;
  }
  const candidate = normalizeIdentity(record.candidate);
  const state = normalizeParamValue(record.state).toLowerCase();
  if (!candidate || !feedbackStates.includes(state as RelatedFeedbackState)) {
    return null;
  }
  const until = normalizeParamValue(record.until);
  return {
    candidate,
    state: state as RelatedFeedbackState,
    until: state === "later" && until !== "" ? until : undefined,
  };
};

export const normalizeRelatedResponse = (payload: unknown): RelatedResponse | null => {
  const record = asRecord(payload);
  if (!record) {
    return null;
  }
  const source = normalizeIdentity(record.source);
  const mode = normalizeRelatedMode(record.mode);
  if (!source || !mode) {
    return null;
  }
  const results = Array.isArray(record.results)
    ? record.results.flatMap((result) => {
        const normalized = normalizeResult(result);
        return normalized ? [normalized] : [];
      })
    : [];
  const feedback = Array.isArray(record.feedback)
    ? record.feedback.flatMap((entry) => {
        const normalized = normalizeFeedback(entry);
        return normalized ? [normalized] : [];
      })
    : [];
  return {
    serverDate: normalizeParamValue(record.server_date),
    source,
    mode,
    hasMore: readBoolean(record.has_more),
    results,
    feedback,
  };
};

export const buildRelatedRequestURL = (
  endpoint: string,
  source: string,
  mode: RelatedMode,
  limit: number
): string => {
  const params = new URLSearchParams();
  params.set("source", source);
  params.set("mode", mode);
  params.set("limit", String(Math.max(1, Math.min(20, Math.trunc(limit) || 1))));
  const separator = endpoint.includes("?") ? "&" : "?";
  return `${endpoint}${separator}${params.toString()}`;
};

const responseErrorMessage = (payload: unknown, status: number): string => {
  const record = asRecord(payload);
  const message = record ? normalizeParamValue(record.message) : "";
  return message || `Related notes request failed (${status}).`;
};

export const requestRelated = async (
  fetcher: RelatedFetch,
  endpoint: string,
  source: string,
  mode: RelatedMode,
  limit: number
): Promise<RelatedResponse> => {
  const response = await fetcher(buildRelatedRequestURL(endpoint, source, mode, limit), {
    headers: { Accept: "application/json" },
  });
  const payload = await readResponsePayload(response);
  if (!response.ok) {
    throw new Error(responseErrorMessage(payload, response.status));
  }
  const normalized = normalizeRelatedResponse(payload);
  if (!normalized || normalized.mode !== mode || normalized.source.key !== source) {
    throw new Error("Related notes returned an invalid response.");
  }
  return normalized;
};
