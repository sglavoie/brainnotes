import { asRecord } from "./api-internal";
import { normalizeParamValue } from "./filters";

export type RelatedMode = "balanced" | "rediscover" | "connect";

export type RelatedNoteIdentity = {
  key: string;
  title: string;
  path: string;
  route: string;
};

export const normalizeIdentity = (value: unknown): RelatedNoteIdentity | null => {
  const record = asRecord(value);
  if (!record) {
    return null;
  }
  const identity = {
    key: normalizeParamValue(record.key),
    title: normalizeParamValue(record.title),
    path: normalizeParamValue(record.path),
    route: normalizeParamValue(record.route),
  };
  return identity.key !== "" && identity.path !== "" && identity.route !== "" ? identity : null;
};

export type RelatedReasonKind = "text" | "graph" | "metadata" | "recall";

export type RelatedReason = {
  kind: RelatedReasonKind;
  detail: string;
};

export type RelatedResult = {
  candidate: RelatedNoteIdentity;
  excerpt: string;
  relationship: "" | "direct" | "shared-neighbor" | "two-hop";
  bridge?: RelatedNoteIdentity;
  reasons: RelatedReason[];
  useful: boolean;
};

export type RelatedFeedbackState = "useful" | "dismissed" | "later";

export type RelatedFeedback = {
  candidate: RelatedNoteIdentity;
  state: RelatedFeedbackState;
  until?: string;
};

export type RelatedResponse = {
  serverDate: string;
  source: RelatedNoteIdentity;
  mode: RelatedMode;
  hasMore: boolean;
  results: RelatedResult[];
  feedback: RelatedFeedback[];
};

export type RelatedConfig = {
  source: string;
  endpoint: string;
  itemEndpoint: string;
  csrfToken: string;
};

export type RelatedFetch = (
  input: RequestInfo | URL,
  init?: RequestInit
) => Promise<Response>;

export type RelatedItemAction =
  | "view"
  | "open_editor"
  | "useful"
  | "dismissed"
  | "later"
  | "clear";

export type RelatedItemRequest = {
  source: string;
  candidate: string;
  action: RelatedItemAction;
  token: string;
  until?: string;
};

export type RelatedItemResponse = {
  ok: boolean;
  action: RelatedItemAction;
  source: RelatedNoteIdentity;
  candidate: RelatedNoteIdentity;
  state?: RelatedFeedbackState | "clear";
  until?: string;
  opened: boolean;
  // destination is the route an Open answered with when the server does not
  // start editors. Empty means a process was started.
  destination?: string;
  recorded?: boolean;
  warning?: string;
};
