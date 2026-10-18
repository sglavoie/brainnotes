import { normalizeManagementErrorMessage, readNotePinResponsePayload } from "./api";
import { asRecord } from "./api-internal";

export type PinOutcome = { kind: "success" | "failure" | "unconfirmed"; message: string };
export const unconfirmedPin = (): PinOutcome => ({
  kind: "unconfirmed",
  message: "Pin change not confirmed. Refresh to check before retrying.",
});

export const readPinOutcome = async (response: Response): Promise<PinOutcome> => {
  const payload = await readNotePinResponsePayload(response);
  if (!response.ok) return { kind: "failure", message: normalizeManagementErrorMessage(response.status, payload) };
  const record = asRecord(payload);
  const message = typeof record?.message === "string" ? record.message : "";
  if (record?.ok === true) return { kind: "success", message };
  if (record?.ok === false) return { kind: "failure", message: message || "The pin change was refused." };
  return unconfirmedPin();
};

export const addPinRefresh = (status: HTMLElement): void => {
  const link = status.ownerDocument.createElement("a");
  link.href = window.location.href;
  link.textContent = "Refresh to check";
  link.addEventListener("click", (event) => {
    if (event.button !== 0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return;
    event.preventDefault();
    window.location.reload();
  });
  status.append(" ", link);
};
