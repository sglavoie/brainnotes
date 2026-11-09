import { loadWorkspaceScript } from "./workspace-script";

export type CollectRequest = { source: string; quote: string; revision: string; returnTo: HTMLElement };
export const collectInThread = async (request: CollectRequest | { returnTo: HTMLElement }): Promise<void> => {
  try {
    await loadWorkspaceScript("thread-collect");
    document.dispatchEvent(new CustomEvent("bn:thread-collect", { detail: request }));
  } catch {
    const status = document.createElement("output");
    status.textContent = "Thread tools unavailable. Retry.";
    request.returnTo.after(status);
  }
};

export const initThreadCollection = (): void => {
  const button = document.querySelector<HTMLElement>("[data-thread-collect]");
  button?.addEventListener("click", () => void collectInThread({ returnTo: button }));
};
