let writingBundle: Promise<void> | undefined;

const loadWriting = (): Promise<void> => {
  writingBundle ||= new Promise<void>((resolve, reject) => {
    const script = document.createElement("script");
    script.src = "/static/connection-writing.js";
    script.onload = () => resolve();
    script.onerror = () => { script.remove(); writingBundle = undefined; reject(new Error("Writing tools unavailable")); };
    document.head.append(script);
  });
  return writingBundle;
};

// Reading never loads creation machinery until the user chooses to write.
export const connectionWriting = (dialog: HTMLDialogElement, sources: string[], signal: AbortSignal): void => {
  const host = document.createElement("section"); host.className = "connection-writing";
  const launch = document.createElement("button");
  launch.type = "button"; launch.className = "bn-button bn-primary connection-draft";
  launch.textContent = "Write a thought about this connection";
  const status = document.createElement("p"); status.setAttribute("role", "status");
  launch.addEventListener("click", () => {
    launch.disabled = true; status.textContent = "Preparing your writing space…";
    void loadWriting().then(() => {
      if (!signal.aborted) document.dispatchEvent(new CustomEvent("bn:connection-writing", { detail: { host, sources, signal } }));
    }).catch(() => {
      if (!signal.aborted) { launch.disabled = false; status.textContent = "Writing tools unavailable. Try again."; }
    });
  });
  host.append(launch, status); dialog.append(host);
};
