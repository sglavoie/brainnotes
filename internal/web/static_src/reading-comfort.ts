const preferences = [
  { selector: "button[data-reading-mode]", key: "bn.reading-mode", attribute: "readingMode", value: "quiet" },
  { selector: "button[data-reading-type]", key: "bn.reading-type", attribute: "readingType", value: "book" },
] as const;

// Presentation preferences never alter the Markdown or the sidebar dimensions.
export const initReadingComfort = (): void => {
  for (const preference of preferences) {
    const button = document.querySelector<HTMLButtonElement>(preference.selector);
    if (!button) continue;
    const root = document.documentElement;
    const update = (): void => {
      button.setAttribute("aria-pressed", String(root.dataset[preference.attribute] === preference.value));
    };
    update();
    button.addEventListener("click", () => {
      const enabled = root.dataset[preference.attribute] !== preference.value;
      root.dataset[preference.attribute] = enabled ? preference.value : "";
      try {
        window.localStorage.setItem(preference.key, enabled ? preference.value : "");
        if (preference.attribute === "readingType" && localStorage.getItem("bn.writing-font") !== "mono") {
          localStorage.setItem("bn.writing-font", enabled ? "prose" : "sans");
        }
      } catch { /* This page still works. */ }
      update();
    });
  }
  initPropertyDisclosure();
  quietRepeatedTitle();

};

const quietRepeatedTitle = (): void => {
  const title = document.querySelector(".reading-title");
  const first = document.querySelector("[data-note-body] > :first-child");
  if (!title || !first) return;
  const heading = first.matches("h1") ? first : first.querySelector("h1");
  if (!heading || heading.textContent.replace(/#\s*$/, "").trim() !== title.textContent.trim()) return;
  // Retain the anchor and source-line marker for passage navigation and printing.
  first.classList.add("note-repeated-title");
  first.setAttribute("aria-hidden", "true");
  first.setAttribute("inert", "");
  for (const link of document.querySelectorAll<HTMLAnchorElement>(".note-toc-list a")) {
    if (decodeURIComponent(link.hash.slice(1)) === heading.id) link.parentElement?.setAttribute("hidden", "");
  }
};

const initPropertyDisclosure = (): void => {
  const details = document.querySelector<HTMLDetailsElement>("[data-note-properties]");
  if (!details) return;
  const key = "bn.reading-properties";
  try { details.open = localStorage.getItem(key) === "expanded"; } catch { /* Compact by default. */ }
  const remember = (): void => {
    try { localStorage.setItem(key, details.open ? "expanded" : "compact"); } catch { /* Still usable. */ }
  };
  details.addEventListener("toggle", remember);
  // Native toggle events are queued; keep a quick navigation from losing the choice.
  window.addEventListener("pagehide", remember);
};
