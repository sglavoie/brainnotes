// @vitest-environment jsdom

import { beforeEach, describe, expect, it } from "vitest";

import {
  contentField,
  element,
  jsonResponse,
  listPayload,
  mount,
  openButtons,
  readPayload,
  statusText,
  templateSummary,
  waitForExpect,
} from "./templates-controller.test-helpers";

beforeEach(() => {
  document.body.innerHTML = "";
});

describe("templates panel: replacing a draft", () => {
  it("asks before an open replaces edited text, and keeps it when declined", async () => {
    const asked: string[] = [];
    const mounted = mount({
      "/api/templates": () => jsonResponse(listPayload([templateSummary()])),
      "/api/templates?name=Daily.md": () => jsonResponse(readPayload()),
    }, (message) => { asked.push(message); return false; });

    await mounted.panel.load();
    contentField(mounted.root).value = "# unsaved\n";
    openButtons(mounted.root)[0].click();
    element<HTMLButtonElement>(mounted.root, "[data-templates-new]").click();

    expect(asked).toHaveLength(2);
    expect(contentField(mounted.root).value).toBe("# unsaved\n");
    expect(mounted.calls.map((call) => call.url)).not.toContain("/api/templates?name=Daily.md");
  });

  it("drops a slow open answered after a later click", async () => {
    let releaseFirst: (value: Response) => void = () => {};
    const other = templateSummary({
      name: "Weekly.md", rel_path: "Templates/Weekly.md", content: "# weekly\n", revision: "sha256:weekly",
    });
    const mounted = mount({
      "/api/templates": () => jsonResponse(listPayload([templateSummary(), other])),
      "/api/templates?name=Daily.md": () => new Promise<Response>((resolve) => { releaseFirst = resolve; }),
      "/api/templates?name=Weekly.md": () =>
        jsonResponse(listPayload([other], { action: "read", name: "Weekly.md", rel_path: "Templates/Weekly.md" })),
    });

    await mounted.panel.load();
    const [daily, weekly] = openButtons(mounted.root);
    daily.click();
    weekly.click();
    await waitForExpect(() => expect(mounted.panel.draft().name).toBe("Weekly.md"));
    releaseFirst(jsonResponse(readPayload()));
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(mounted.panel.draft().name).toBe("Weekly.md");
    expect(contentField(mounted.root).value).toBe("# weekly\n");
  });

  it("refuses to preview a save while a template is still opening", async () => {
    const mounted = mount({
      "/api/templates": () => jsonResponse(listPayload([templateSummary()])),
      "/api/templates?name=Daily.md": () => new Promise<Response>(() => {}),
    });

    await mounted.panel.load();
    openButtons(mounted.root)[0].click();
    element<HTMLButtonElement>(mounted.root, "[data-templates-save]").click();
    expect(statusText(mounted.root)).toBe("Wait for the template to finish opening.");
    expect(mounted.calls.map((call) => call.url)).not.toContain("/api/templates/preview");
  });
});
