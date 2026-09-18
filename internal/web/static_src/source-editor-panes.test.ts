// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import { readSourceEditorElements, type SourceEditorElements } from "./source-editor-elements";
import { initSourcePanes, type SourcePaneName, type SourcePanes } from "./source-editor-panes";
import { editorMarkup } from "./source-editor.test-helpers";

// The tab pair on its own, without the controller around it.
//
// What is worth pinning here is the keyboard contract and the fact that the
// selection is *published* rather than applied: css/responsive.css turns the
// same markup into two columns above 960px by overriding the rule keyed on
// `data-source-active`, which it can only do because nothing in here sets
// `hidden` on a pane.

type Harness = {
  elements: SourceEditorElements;
  panes: SourcePanes;
  tab(name: SourcePaneName): HTMLButtonElement;
  container(): HTMLElement;
  selections: SourcePaneName[];
};

const mount = (): Harness => {
  document.body.innerHTML = editorMarkup();
  const panel = document.querySelector<HTMLElement>("[data-source-editor]");
  const elements = panel ? readSourceEditorElements(panel) : null;
  if (!elements) {
    throw new Error("fixture does not describe a usable editor");
  }

  const panes = initSourcePanes(elements);
  const selections: SourcePaneName[] = [];
  panes.onSelect((pane) => selections.push(pane));

  return {
    elements,
    panes,
    selections,
    tab: (name) => {
      const tab = document.querySelector<HTMLButtonElement>(`[data-source-tab="${name}"]`);
      if (!tab) {
        throw new Error(`fixture is missing the ${name} tab`);
      }
      return tab;
    },
    container: () => elements.paneContainer,
  };
};

const press = (tab: HTMLButtonElement, key: string): KeyboardEvent => {
  const event = new KeyboardEvent("keydown", { key, cancelable: true, bubbles: true });
  tab.dispatchEvent(event);
  return event;
};

afterEach(() => {
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

describe("the initial state", () => {
  it("starts on the editor with the selection published", () => {
    const harness = mount();

    expect(harness.panes.active()).toBe("edit");
    expect(harness.container().getAttribute("data-source-active")).toBe("edit");
    expect(harness.tab("edit").getAttribute("aria-selected")).toBe("true");
    expect(harness.tab("preview").getAttribute("aria-selected")).toBe("false");
  });

  // The attribute goes on the element the template names, not on whatever
  // happens to be a pane's parent. css/responsive.css selects
  // `.source-editor-panes[data-source-active]` — one element, by name — so
  // deriving the target from a pane's parent puts it wherever the markup last
  // nested the panes, and a wrapper introduced between the two would move it
  // somewhere no rule looks.
  //
  // The wrapper is inserted here rather than described in the fixture, because
  // the fixture has to keep mirroring the template: with the panes still direct
  // children, `parentElement` and the marker are the same element and this
  // would pass against either implementation.
  it("publishes onto the element the template marked, not a pane's parent", () => {
    document.body.innerHTML = editorMarkup();
    const marked = document.querySelector<HTMLElement>("[data-source-panes]");
    const panel = document.querySelector<HTMLElement>("[data-source-editor]");
    if (!marked || !panel) {
      throw new Error("fixture does not describe a usable editor");
    }
    const wrapper = document.createElement("div");
    wrapper.append(...marked.children);
    marked.append(wrapper);

    const elements = readSourceEditorElements(panel);
    if (!elements) {
      throw new Error("fixture does not describe a usable editor");
    }
    initSourcePanes(elements).select("preview");

    expect(elements.paneContainer).toBe(marked);
    expect(marked.getAttribute("data-source-active")).toBe("preview");
    expect(wrapper.hasAttribute("data-source-active")).toBe(false);
  });

  // Hiding a pane from script would win over every stylesheet, and the wide
  // layout — which shows both — would then need a second, resize-driven code
  // path in here to undo it.
  it("never hides a pane itself", () => {
    const harness = mount();
    harness.panes.select("preview");

    for (const pane of harness.elements.panes) {
      expect(pane.hidden).toBe(false);
    }
  });
});

describe("selection", () => {
  it("moves on a click and tells its listeners once", () => {
    const harness = mount();

    harness.tab("preview").click();
    expect(harness.panes.active()).toBe("preview");
    expect(harness.selections).toEqual(["preview"]);
  });

  // onSelect exists so the controller can flush a render when the pane becomes
  // visible. Firing it on a click that changed nothing would make every click
  // on the showing tab a request.
  it("says nothing when the chosen pane is already showing", () => {
    const harness = mount();

    harness.tab("edit").click();
    harness.panes.select("edit");
    expect(harness.selections).toEqual([]);
  });
});

describe("the keyboard", () => {
  // A tablist is one tab stop: the selected tab is reachable by Tab and the
  // others by arrow keys. That is what roving tabindex means, and a pair left
  // at 0 puts a dead stop in the page's tab order.
  it("keeps exactly one tab in the page's tab order", () => {
    const harness = mount();
    expect(harness.tab("edit").tabIndex).toBe(0);
    expect(harness.tab("preview").tabIndex).toBe(-1);

    harness.panes.select("preview");
    expect(harness.tab("edit").tabIndex).toBe(-1);
    expect(harness.tab("preview").tabIndex).toBe(0);
  });

  it("moves to the next tab on ArrowRight and focuses it", () => {
    const harness = mount();

    const event = press(harness.tab("edit"), "ArrowRight");
    expect(event.defaultPrevented).toBe(true);
    expect(harness.panes.active()).toBe("preview");
    expect(document.activeElement).toBe(harness.tab("preview"));
  });

  // Wrapping is the point: with two tabs, ArrowRight from the last one is the
  // ordinary way back to the first.
  it("wraps in both directions", () => {
    const harness = mount();

    press(harness.tab("edit"), "ArrowLeft");
    expect(harness.panes.active()).toBe("preview");

    press(harness.tab("preview"), "ArrowRight");
    expect(harness.panes.active()).toBe("edit");
  });

  it("goes to the ends on Home and End", () => {
    const harness = mount();

    press(harness.tab("edit"), "End");
    expect(harness.panes.active()).toBe("preview");

    press(harness.tab("preview"), "Home");
    expect(harness.panes.active()).toBe("edit");
  });

  // Every other key has to reach the browser. Swallowing Tab here would trap a
  // keyboard user in the tablist, which is the same trap the editor refuses to
  // set by leaving Tab unbound in its own keymap.
  it("leaves every other key alone", () => {
    const harness = mount();

    for (const key of ["Tab", "ArrowUp", "ArrowDown", "a"]) {
      const event = press(harness.tab("edit"), key);
      expect(event.defaultPrevented).toBe(false);
    }
    expect(harness.panes.active()).toBe("edit");
  });
});
