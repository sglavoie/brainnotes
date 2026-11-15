const storageKey = "bn.shortcuts";

export const initShortcutPreference = (doc: Document): void => {
  const toggle = doc.querySelector<HTMLInputElement>("[data-shortcuts-toggle]");
  const apply = (enabled: boolean): void => {
    doc.documentElement.dataset.shortcuts = enabled ? "on" : "off";
    if (toggle) toggle.checked = enabled;
  };
  const read = (): void => {
    try { apply(doc.defaultView?.localStorage.getItem(storageKey) !== "off"); }
    catch { apply(true); }
  };
  read();
  toggle?.addEventListener("change", () => {
    apply(toggle.checked);
    try { doc.defaultView?.localStorage.setItem(storageKey, toggle.checked ? "on" : "off"); }
    catch { /* The current-page preference still applies without storage. */ }
  });
  doc.defaultView?.addEventListener("storage", (event) => {
    if (event.key === storageKey || event.key === null) read();
  });
};
