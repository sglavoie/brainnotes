import { asRecord } from "./api-internal";
import { normalizeManagementErrorMessage } from "./api-notes";

type ResponseInfo = { ok: boolean; status: number; redirected?: boolean };

export type RequestPurpose = "read" | "preview" | "write";
export type RequestFailure = { ok: false; message: string; uncertain?: boolean };

export const unansweredRequest = (purpose: RequestPurpose): RequestFailure => ({
  ok: false,
  uncertain: purpose === "write",
  message: purpose === "write"
    ? "Couldn't confirm the change. It may have been saved. Check the current values or History in another tab before trying again. If sign-in expired, sign in again first."
    : purpose === "preview"
      ? "Couldn't load the preview. No change was submitted. Your inputs are still here; check your connection or sign in again, then retry the preview."
      : "Couldn't load the current values. Check your connection or sign in again, then refresh to retry.",
});

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
