import type { SourceEditorElements } from "./source-editor-elements";

// The edit/preview pane pair, and the one decision behind it.
//
// Selection is published as `data-source-active` on the container rather than
// by hiding a pane from script. On a narrow screen the two are tabs and CSS
// hides the unselected one; on a wide screen the same markup is two columns and
// css/responsive.css shows both, overriding the selection. Had the controller
// set `hidden` there would be no way for a stylesheet to say "actually, show
// both" — the attribute wins over every rule — and the wide layout would need a
// second, resize-driven code path in here to undo what this one did.

export type SourcePaneName = "edit" | "preview";

export type SourcePanes = {
  active(): SourcePaneName;
  select(pane: SourcePaneName): void;
  // onSelect fires only on a change, so switching to the preview can flush a
  // render without every click re-rendering.
  onSelect(listener: (pane: SourcePaneName) => void): void;
};

export const initSourcePanes = (elements: SourceEditorElements): SourcePanes => {
  const container = elements.paneContainer;
  const listeners: ((pane: SourcePaneName) => void)[] = [];
  let active: SourcePaneName = "edit";

  const apply = (pane: SourcePaneName): void => {
    active = pane;
    container.setAttribute("data-source-active", pane);
    for (const tab of elements.tabs) {
      const selected = tab.getAttribute("data-source-tab") === pane;
      tab.setAttribute("aria-selected", selected ? "true" : "false");
      // A tablist is one tab stop: the selected tab is reachable by Tab and the
      // others by arrow keys, which is what roving tabindex means.
      tab.tabIndex = selected ? 0 : -1;
    }
  };

  const select = (pane: SourcePaneName): void => {
    if (pane === active) {
      return;
    }
    apply(pane);
    for (const listener of listeners) {
      listener(pane);
    }
  };

  for (const [index, tab] of elements.tabs.entries()) {
    tab.addEventListener("click", () => select(paneName(tab.getAttribute("data-source-tab"))));
    tab.addEventListener("keydown", (event) => {
      const next = arrowTarget(event, index, elements.tabs.length);
      if (next === null) {
        return;
      }
      event.preventDefault();
      const target = elements.tabs[next];
      target.focus();
      select(paneName(target.getAttribute("data-source-tab")));
    });
  }

  apply(active);

  return {
    active: () => active,
    select,
    onSelect(listener) {
      listeners.push(listener);
    },
  };
};

const paneName = (value: string | null): SourcePaneName =>
  value === "preview" ? "preview" : "edit";

const arrowTarget = (event: KeyboardEvent, index: number, count: number): number | null => {
  if (count === 0) {
    return null;
  }
  switch (event.key) {
    case "ArrowRight":
      return (index + 1) % count;
    case "ArrowLeft":
      return (index - 1 + count) % count;
    case "Home":
      return 0;
    case "End":
      return count - 1;
    default:
      return null;
  }
};
