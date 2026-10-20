// @vitest-environment jsdom

import { afterEach, describe, expect, it } from "vitest";

import { initPropertyDetail } from "./properties";
import { createWorkbenchEditor } from "./properties-workbench-editor";
import {
  detailPayload,
  mountWorkbench,
  requireElement,
  routedFetch,
  waitForExpect,
} from "./properties-workbench.test-helpers";

afterEach(() => {
  document.body.innerHTML = "";
});

const setup = () => {
  document.body.innerHTML = `
    <div data-property-row="Notes/A.md">
      <button type="button" data-property-row-edit="Notes/A.md">Edit</button>
    </div>
    <div data-editor-host></div>`;
  const opener = requireElement("[data-property-row-edit]", HTMLButtonElement);
  const host = requireElement("[data-editor-host]", HTMLElement);
  const editor = createWorkbenchEditor(document);
  const open = (): HTMLInputElement => {
    opener.focus();
    let input: HTMLInputElement | null = null;
    editor.mount(host, "Edit", "Save", (form) => {
      input = document.createElement("input");
      form.appendChild(input);
      return { submit: () => null, focus: () => input?.focus() };
    });
    if (!input) {
      throw new Error("input missing");
    }
    return input;
  };
  return { editor, host, open, opener };
};

describe("workbench editor focus", () => {
  it("focuses the form's control once it is mounted", () => {
    const { open } = setup();
    const input = open();
    expect(input.isConnected).toBe(true);
    expect(document.activeElement).toBe(input);
  });

  it("returns focus to the opener on Escape and on Cancel", () => {
    const { open, opener } = setup();
    open().dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    expect(document.querySelector("[data-property-editor-form]")).toBeNull();
    expect(document.activeElement).toBe(opener);

    open();
    requireElement("[data-property-editor-cancel]", HTMLButtonElement).click();
    expect(document.activeElement).toBe(opener);
  });

  it("refocuses the opener's replacement after the rows are repainted", () => {
    const { editor, open } = setup();
    open();
    editor.reset();
    document.body.innerHTML = `
      <div data-property-row="Notes/A.md">
        <button type="button" data-property-row-edit="Notes/A.md">Edit</button>
      </div>`;
    editor.refocus();
    expect(document.activeElement).toBe(
      requireElement("[data-property-row-edit]", HTMLButtonElement)
    );
  });

  it("falls back to the row when the replacement control is disabled", () => {
    const { editor, open } = setup();
    open();
    editor.reset();
    document.body.innerHTML = `
      <div data-property-row="Notes/A.md" tabindex="-1">
        <button type="button" data-property-row-edit="Notes/A.md" disabled>Edit</button>
      </div>`;
    editor.refocus();
    expect(document.activeElement).toBe(requireElement("[data-property-row]", HTMLElement));
  });
});

describe("property detail editor focus", () => {
  it("moves focus into the row editor and back to its Edit button", async () => {
    mountWorkbench();
    initPropertyDetail({ fetch: routedFetch({ "/api/properties/detail": () => detailPayload() }) });
    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-property-row-edit]")).toHaveLength(2);
    });
    const edit = requireElement("[data-property-row-edit='Notes/Alpha.md']", HTMLButtonElement);
    edit.focus();
    edit.click();
    const control = requireElement("[data-property-control]", HTMLSelectElement);
    expect(document.activeElement).toBe(control);

    control.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    expect(document.activeElement).toBe(edit);
  });
});
