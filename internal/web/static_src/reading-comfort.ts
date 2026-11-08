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
      try { window.localStorage.setItem(preference.key, enabled ? preference.value : ""); } catch { /* This page still works. */ }
      update();
    });
  }
  quietRepeatedTitle();
  arrangeReadingControls();
};

const arrangeReadingControls = (): void => {
  const controls = document.querySelector<HTMLElement>(".reading-controls");
  const menu = document.querySelector(".note-more-options");
  if (!controls || !menu) return;
  const marker = document.createComment("reading preferences");
  controls.before(marker);
  const narrow = window.matchMedia("(max-width: 700px)");
  const arrange = (): void => {
    if (narrow.matches) menu.prepend(controls);
    else marker.after(controls);
  };
  narrow.addEventListener("change", arrange);
  arrange();
};

const quietRepeatedTitle = (): void => {
  const title = document.querySelector(".note-main > header h1");
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
