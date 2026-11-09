// Keep the live status mounted for assistive technology, beside its destination.
// Earlier receipts remain available without expanding the everyday capture flow.
export const initCaptureFeedback = (root: HTMLElement, status: HTMLElement, recent: HTMLElement): (() => void) => {
  const receipt = document.createElement("div");
  receipt.className = "capture-receipt";
  status.before(receipt);
  receipt.append(status);
  const link = root.querySelector<HTMLAnchorElement>("[data-capture-view-inbox]");
  if (link) { link.textContent = "View inbox"; receipt.append(link); }
  const history = document.createElement("details");
  history.className = "capture-history";
  const summary = document.createElement("summary");
  summary.textContent = "Captured this session";
  history.append(summary);
  recent.before(history);
  history.append(recent);
  history.hidden = true;
  root.querySelector("[data-capture-input]")?.addEventListener("input", () => { delete root.dataset.captureSettled; });
  return () => {
    root.dataset.captureSettled = "true";
    history.hidden = recent.children.length === 0;
    summary.textContent = `Captured this session (${recent.children.length})`;
  };
};
