// Wait for the surface before opening a dialog, including on slow connections.
export const loadThoughtStyles = (): Promise<void> => new Promise((resolve, reject) => {
  const existing = document.querySelector<HTMLLinkElement>('link[href="/static/thought-tools.css"]');
  if (existing?.sheet) { resolve(); return; }
  const style = existing || document.createElement("link");
  style.addEventListener("load", () => resolve(), { once: true });
  style.addEventListener("error", () => { style.remove(); reject(new Error("Writing styles unavailable")); }, { once: true });
  if (!existing) { style.rel = "stylesheet"; style.href = "/static/thought-tools.css"; document.head.append(style); }
});
