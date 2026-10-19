// A <details> used as a popover keeps native toggling, so it works before and
// without this script. These helpers add the dismissals people expect of a
// menu and return focus to the summary that opened it.

const summaryOf = (details: HTMLDetailsElement): HTMLElement | null =>
  details.querySelector<HTMLElement>(":scope > summary");

// Escape closes the disclosure unless a control inside it already used the
// key, such as a search field clearing its suggestions.
export const closeDisclosureOnEscape = (details: HTMLDetailsElement): void => {
  details.addEventListener("keydown", (event) => {
    if (event.key !== "Escape" || event.defaultPrevented || !details.open) return;
    event.preventDefault();
    details.open = false;
    summaryOf(details)?.focus();
  });
};

// A press outside closes the disclosure. The press normally chooses the next
// focus itself; only when it lands on nothing focusable, which would leave
// focus inside the now hidden content, does focus return to the summary.
export const closeDisclosureOnOutsidePointer = (details: HTMLDetailsElement): void => {
  const doc = details.ownerDocument;
  doc.addEventListener("pointerdown", (event) => {
    if (!details.open || (event.target instanceof Node && details.contains(event.target))) return;
    const hadFocus = details.contains(doc.activeElement);
    details.open = false;
    if (!hadFocus) return;
    doc.defaultView?.setTimeout(() => {
      const active = doc.activeElement;
      if (!active || active === doc.body || details.contains(active)) summaryOf(details)?.focus();
    }, 0);
  });
};

export const initWorkspacePopovers = (doc: Document = document): void => {
  for (const details of doc.querySelectorAll<HTMLDetailsElement>("details.source-editor-workspace")) {
    closeDisclosureOnEscape(details);
    closeDisclosureOnOutsidePointer(details);
  }
};
