import { loadWriting, type ThoughtWritingRequest } from "./connection-writing-loader";

export const startConnectionWriting = (request: ThoughtWritingRequest): void => {
  const { host, signal } = request;
  const status = document.createElement("p"); status.setAttribute("role", "status");
  status.textContent = "Preparing your writing space…"; host.replaceChildren(status);
  void loadWriting().then(() => {
    if (!signal.aborted) document.dispatchEvent(new CustomEvent("bn:connection-writing", { detail: request }));
  }).catch(() => {
    if (signal.aborted) return;
    status.textContent = "Writing tools unavailable. Your passage is kept here.";
    const retry = document.createElement("button"); retry.type = "button"; retry.className = "bn-button"; retry.textContent = "Retry writing space";
    retry.addEventListener("click", () => startConnectionWriting(request)); host.append(retry);
  });
};

