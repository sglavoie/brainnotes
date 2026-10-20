// @vitest-environment jsdom

import { afterEach, describe, expect, it } from "vitest";

import { initWorkspacePopovers } from "./disclosure-popover";

afterEach(() => {
  document.body.innerHTML = "";
});

const mount = (): { details: HTMLDetailsElement; summary: HTMLElement; link: HTMLAnchorElement; outside: HTMLElement } => {
  document.body.innerHTML = `
    <details class="source-editor-workspace" open>
      <summary>Workspace</summary>
      <div class="source-editor-workspace-content"><a href="/library">Library</a></div>
    </details>
    <p data-outside>Elsewhere</p>
  `;
  initWorkspacePopovers(document);
  const details = document.querySelector<HTMLDetailsElement>("details");
  const summary = document.querySelector<HTMLElement>("summary");
  const link = document.querySelector<HTMLAnchorElement>("a");
  const outside = document.querySelector<HTMLElement>("[data-outside]");
  if (!details || !summary || !link || !outside) throw new Error("fixture missing");
  return { details, summary, link, outside };
};

describe("workspace popover", () => {
  it("closes on Escape and returns focus to its summary", () => {
    const { details, summary, link } = mount();
    link.focus();
    link.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
    expect(details.open).toBe(false);
    expect(document.activeElement).toBe(summary);
  });

  it("leaves Escape to a control that already handled it", () => {
    const { details, link } = mount();
    link.addEventListener("keydown", (event) => event.preventDefault());
    link.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
    expect(details.open).toBe(true);
  });

  it("closes on a press outside and returns stranded focus to the summary", async () => {
    const { details, summary, link, outside } = mount();
    link.focus();
    outside.dispatchEvent(new Event("pointerdown", { bubbles: true }));
    expect(details.open).toBe(false);
    link.blur();
    await new Promise((done) => setTimeout(done, 0));
    expect(document.activeElement).toBe(summary);
  });

  it("stays open for a press inside", () => {
    const { details, link } = mount();
    link.dispatchEvent(new Event("pointerdown", { bubbles: true }));
    expect(details.open).toBe(true);
  });

  it("closes when focus moves to something outside", () => {
    const { details, link, outside } = mount();
    link.dispatchEvent(new FocusEvent("focusout", { bubbles: true, relatedTarget: outside }));
    expect(details.open).toBe(false);
  });

  it("stays open when focus moves within it or leaves the page", () => {
    const { details, summary, link } = mount();
    link.dispatchEvent(new FocusEvent("focusout", { bubbles: true, relatedTarget: summary }));
    expect(details.open).toBe(true);
    link.dispatchEvent(new FocusEvent("focusout", { bubbles: true, relatedTarget: null }));
    expect(details.open).toBe(true);
  });
});
