// A shared DOM receipt prevents separately bundled entry points from loading
// the same workspace twice and registering duplicate creation listeners.
export const loadWorkspaceScript = (name: string): Promise<void> => new Promise((resolve, reject) => {
  const src = `/static/${name}.js`;
  const existing = document.querySelector<HTMLScriptElement>(`script[src="${src}"]`);
  if (existing?.dataset.ready) { resolve(); return; }
  const script = existing || document.createElement("script");
  script.addEventListener("load", () => { script.dataset.ready = "true"; resolve(); }, { once: true });
  script.addEventListener("error", () => { script.remove(); reject(new Error("Workspace tools unavailable")); }, { once: true });
  if (!existing) { script.src = src; document.head.append(script); }
});
