// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { initSavedViewsSave } from "./saved-views";
import { dispatchFiltersChange } from "./filters";
import {
  jsonResponse,
  savePayload,
  mountSave,
  saveElement,
  submitSave,
  setLocation,
} from "./saved-views.test-helpers";


beforeEach(() => {
  setLocation("/library");
});

afterEach(() => {
  document.body.innerHTML = "";
  vi.unstubAllGlobals();
});

/**
 * The two halves of the saved-views write class, seen from the DOM: an
 * overwrite renders its diff as a question with the confirm button showing,
 * while a creation renders the same diff as a receipt with nothing to click.
 */
describe("saved views save scope", () => {
  it("shows the saved shape and updates excluded filters while the form is open", () => {
    mountSave();
    setLocation("/library?expr=tag:focus&sort=-modified&limit=10&q=words&tag=work&tag=books");
    initSavedViewsSave();
    saveElement("[data-saved-views-open]", HTMLButtonElement).click();
    const scope = saveElement("[data-saved-views-scope]", HTMLElement);
    const warning = saveElement("[data-saved-views-excluded]", HTMLElement);
    expect(scope.textContent).toBe("This view will save:\nQuery: tag:focus\nSort: -modified\nLimit: 10");
    expect(warning.hidden).toBe(false);
    expect(warning.textContent).toContain("Text search: words; tag: work; tag: books");

    setLocation("/library?expr=tag:focus");
    dispatchFiltersChange(window, { mode: "search", params: new URLSearchParams("expr=tag:focus") });
    expect(warning.hidden).toBe(true);
    expect(warning.textContent).toBe("");
    expect(scope.textContent).toContain("Sort: default\nLimit: none");

    setLocation("/library?q=%3Cb%3Etext%3C%2Fb%3E");
    document.body.insertAdjacentHTML("beforeend", '<input id="search-input">');
    document.querySelector("#search-input")?.dispatchEvent(new Event("input", { bubbles: true }));
    expect(warning.hidden).toBe(false);
    expect(warning.textContent).toContain("<b>text</b>");
    expect(warning.querySelector("b")).toBeNull();
    expect(scope.textContent).toContain("write a query expression first");
  });
});

describe("saved views save form", () => {
  it("renders the creation diff as a non-actionable receipt after the auto-applied write", async () => {
    mountSave();
    setLocation("/library?expr=tag%3Afocus");
    const routes: Record<string, () => Response> = {
      "/api/views/preview": () => jsonResponse(savePayload(true, { preview_id: "receipt-1" })),
      "/api/views/apply": () => jsonResponse(savePayload(true, { journal_id: "j-1" })),
    };
    const saver = initSavedViewsSave({
      fetch: async (input) => {
        const route = routes[String(input)];
        if (!route) {
          throw new Error(`unexpected request ${String(input)}`);
        }
        return route();
      },
    });

    await submitSave("Focus");

    const diff = saveElement("[data-saved-views-diff]", HTMLPreElement);
    expect(diff.hidden).toBe(false);
    expect(diff.getAttribute("data-saved-views-diff-role")).toBe("receipt");
    expect(diff.textContent).toBe('+ [[views]]\n+ name = "Focus"');
    // Nothing on the receipt can re-apply the preview the write consumed.
    expect(saveElement("[data-saved-views-confirm]", HTMLButtonElement).hidden).toBe(true);
    expect(saver?.pending()).toBeNull();
    expect(saveElement("[data-saved-views-status]", HTMLParagraphElement).textContent).toBe(
      'Saved view "Focus".'
    );
  });

  it("renders an overwrite diff as a confirmation instead, and writes only once confirmed", async () => {
    mountSave();
    setLocation("/library?expr=tag%3Afocus");
    const calls: string[] = [];
    const saver = initSavedViewsSave({
      fetch: async (input) => {
        calls.push(String(input));
        if (String(input) === "/api/views/preview") {
          return jsonResponse(savePayload(false, { preview_id: "receipt-1" }));
        }
        return jsonResponse(savePayload(false, { journal_id: "j-1" }));
      },
    });

    await submitSave("Focus");

    const diff = saveElement("[data-saved-views-diff]", HTMLPreElement);
    expect(diff.getAttribute("data-saved-views-diff-role")).toBe("confirm");
    expect(saveElement("[data-saved-views-confirm]", HTMLButtonElement).hidden).toBe(false);
    expect(calls).toEqual(["/api/views/preview"]);
    expect(saver?.pending()?.previewID).toBe("receipt-1");

    saveElement("[data-saved-views-confirm]", HTMLButtonElement).click();
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(calls).toEqual(["/api/views/preview", "/api/views/apply"]);
    expect(diff.hidden).toBe(true);
    expect(saveElement("[data-saved-views-confirm]", HTMLButtonElement).hidden).toBe(true);
  });

  it("clears a creation receipt when the save panel is closed", async () => {
    mountSave();
    setLocation("/library?expr=tag%3Afocus");
    initSavedViewsSave({
      fetch: async (input) =>
        jsonResponse(
          savePayload(true, {
            preview_id: String(input) === "/api/views/preview" ? "receipt-1" : undefined,
          })
        ),
    });

    await submitSave("Focus");
    const diff = saveElement("[data-saved-views-diff]", HTMLPreElement);
    expect(diff.hidden).toBe(false);

    saveElement("[data-saved-views-cancel]", HTMLButtonElement).click();

    expect(diff.hidden).toBe(true);
    expect(diff.hasAttribute("data-saved-views-diff-role")).toBe(false);
  });
});
