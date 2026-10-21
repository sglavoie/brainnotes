// @vitest-environment jsdom

import { beforeEach, describe, expect, it } from "vitest";

import {
  contentField,
  element,
  jsonResponse,
  listPayload,
  mount,
  openButtons,
  previewPayload,
  readPayload,
  statusText,
  templateSummary,
  waitForExpect,
} from "./templates-controller.test-helpers";

beforeEach(() => {
  document.body.innerHTML = "";
});

type Deferred = { promise: Promise<Response>; resolve: (response: Response) => void };

const deferred = (): Deferred => {
  let resolve: (response: Response) => void = () => {};
  const promise = new Promise<Response>((settle) => {
    resolve = settle;
  });
  return { promise, resolve };
};

const later = (gate: Deferred) => () => gate.promise;

const refresh = (root: HTMLElement): void =>
  element<HTMLButtonElement>(root, "[data-templates-refresh]").click();

const listCalls = (calls: { url: string }[]): number =>
  calls.filter((call) => call.url === "/api/templates").length;

describe("templates panel: refreshing", () => {
  it("leaves a confirmation in flight alone", async () => {
    const apply = deferred();
    const mounted = mount({
      "/api/templates": () => jsonResponse(listPayload([templateSummary()])),
      "/api/templates?name=Daily.md": () => jsonResponse(readPayload()),
      "/api/templates/preview": () => jsonResponse(previewPayload(false)),
      "/api/templates/apply": later(apply),
    });
    await mounted.panel.load();
    openButtons(mounted.root)[0].click();
    await waitForExpect(() => expect(mounted.panel.draft().revision).toBe("sha256:old"));
    contentField(mounted.root).value = "# edited\n";
    element<HTMLButtonElement>(mounted.root, "[data-templates-save]").click();
    await waitForExpect(() => expect(mounted.panel.pending()).not.toBeNull());

    element<HTMLButtonElement>(mounted.root, "[data-templates-confirm]").click();
    await waitForExpect(() => expect(statusText(mounted.root)).toBe('Saving "Daily.md"…'));
    refresh(mounted.root);

    expect(listCalls(mounted.calls)).toBe(1);
    expect(contentField(mounted.root).disabled).toBe(true);
    expect(element<HTMLButtonElement>(mounted.root, "[data-templates-confirm]").hidden).toBe(false);
    expect(statusText(mounted.root)).toBe('Saving "Daily.md"…');

    apply.resolve(
      jsonResponse(listPayload([], { action: "apply", name: "Daily.md", revision: "sha256:new" }))
    );
    await waitForExpect(() => expect(mounted.panel.draft().revision).toBe("sha256:new"));
  });

  it("drops a waiting receipt through the writer, not just the view", async () => {
    const mounted = mount({
      "/api/templates": () => jsonResponse(listPayload([templateSummary()])),
      "/api/templates?name=Daily.md": () => jsonResponse(readPayload()),
      "/api/templates/preview": () => jsonResponse(previewPayload(false)),
    });
    await mounted.panel.load();
    openButtons(mounted.root)[0].click();
    await waitForExpect(() => expect(mounted.panel.draft().revision).toBe("sha256:old"));
    element<HTMLButtonElement>(mounted.root, "[data-templates-save]").click();
    await waitForExpect(() => expect(mounted.panel.pending()).not.toBeNull());

    refresh(mounted.root);
    expect(mounted.panel.pending()).toBeNull();
    expect(contentField(mounted.root).disabled).toBe(false);
    await waitForExpect(() => expect(statusText(mounted.root)).toBe("1 templates in Templates/."));
  });

  it("paints only the latest listing", async () => {
    const gates = [deferred(), deferred()];
    let issued = 0;
    const mounted = mount({
      "/api/templates": () => gates[issued++]?.promise ?? Promise.reject(new Error("extra listing")),
    });
    const first = mounted.panel.load();
    const second = mounted.panel.load();
    gates[1]?.resolve(jsonResponse(listPayload([templateSummary({ name: "New.md" })])));
    await second;
    gates[0]?.resolve(jsonResponse(listPayload([])));
    await first;

    expect(openButtons(mounted.root).map((button) => button.textContent)).toEqual(["New.md"]);
    expect(statusText(mounted.root)).toBe("1 templates in Templates/.");
  });
});
