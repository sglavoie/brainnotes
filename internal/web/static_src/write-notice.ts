// Signals confirmed writes across separately loaded workspaces. This carries no
// note content and requests a fresh status read; it never predicts sync progress.
export const confirmedWriteEvent = "bn:writeconfirmed";

export const announceConfirmedWrite = (): void => {
  if (typeof window !== "undefined" && typeof window.dispatchEvent === "function") window.dispatchEvent(new Event(confirmedWriteEvent));
};
