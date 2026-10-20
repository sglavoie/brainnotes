import { asRecord } from "./api-internal";
import { normalizeManagementErrorMessage } from "./api-notes";

type ResponseInfo = { ok: boolean; status: number; redirected?: boolean };

export type RequestPurpose = "read" | "preview" | "write";
export type RequestFailure = { ok: false; message: string; uncertain?: boolean };

export const unansweredRequest = (purpose: RequestPurpose): RequestFailure => ({
  ok: false,
  uncertain: purpose === "write",
  message: purpose === "write"
    ? "Change not confirmed. Check the current values or History in another tab before retrying."
    : purpose === "preview"
      ? "Preview unavailable. No change was submitted. Your inputs are still here; retry the preview."
      : "Couldn't load the current values. Check your connection, then refresh. If sign-in expired, sign in again.",
});

export const serverUnreachableMessage =
  "Couldn't reach the bn server. Check it is still running, then retry.";

// fetch rejects with a TypeError when no response arrives at all. Its wording
// ("Failed to fetch", "Load failed", "NetworkError…") varies by browser and
// tells a reader nothing, so it is never shown as is.
export const isUnreachableError = (error: unknown): boolean => error instanceof TypeError;

// requestErrorMessage turns a rejected request into one sentence: a network
// failure says the server could not be reached, any other error keeps its own
// message, and an empty one falls back to the caller's wording.
export const requestErrorMessage = (error: unknown, fallback: string): string => {
  if (isUnreachableError(error)) return serverUnreachableMessage;
  if (typeof error === "string" && error.trim() !== "") return error.trim();
  if (error instanceof Error && error.message.trim() !== "") return error.message.trim();
  return fallback;
};

// A list or queue that failed to load says what is missing, the likely cause,
// and which control on screen loads it again.
export const loadFailureMessage = (subject: string, retryLabel: string): string =>
  `Couldn't load ${subject}. Check your connection, then select ${retryLabel}.`;

// A redirect, HTML login page, truncated JSON or malformed envelope cannot
// establish whether a write happened. Never turn these into a claimed refusal.
export const responseFailure = (
  response: ResponseInfo, payload: unknown, purpose: RequestPurpose
): RequestFailure | null => {
  if (response.redirected) return unansweredRequest(purpose);
  const record = asRecord(payload);
  // Some existing refusal envelopes carry code/message without an ok field.
  if (!response.ok && typeof record?.message === "string") {
    return { ok: false, message: record.message };
  }
  if (typeof record?.ok !== "boolean") return unansweredRequest(purpose);
  if (!response.ok || record.ok === false) {
    return { ok: false, message: refusalMessage(response, record, purpose) };
  }
  return null;
};

const refusalMessage = (response: ResponseInfo, record: Record<string, unknown>, purpose: RequestPurpose): string => {
  if (record.message) return normalizeManagementErrorMessage(response.status, record);
  return purpose === "write" ? "The change was refused. Check the current values before trying again."
    : "The request was refused. Review your inputs and try again.";
};
