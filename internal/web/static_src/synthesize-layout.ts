import { element } from "./synthesize-view-elements";

export const synthesisDisclosure = (doc: Document, label: string, ...children: HTMLElement[]): HTMLDetailsElement => {
  const details = element(doc, "details", "bn-disclosure synthesize-disclosure");
  const summary = element(doc, "summary", "");
  summary.textContent = label;
  details.append(summary, ...children);
  return details;
};

export const synthesisNavigation = (doc: Document, targets: [string, HTMLElement][]): HTMLElement => {
  const nav = element(doc, "nav", "synthesize-navigation", { "aria-label": "Synthesis sections" });
  for (const [label, target] of targets) {
    target.id = `synthesis-${label.toLowerCase()}`;
    target.tabIndex = -1;
    const link = element(doc, "a", "bn-button");
    link.href = `#${target.id}`;
    link.textContent = label;
    link.addEventListener("click", () => {
      if (target instanceof HTMLDetailsElement) target.open = true;
      target.focus({ preventScroll: true });
    });
    nav.append(link);
  }
  return nav;
};
