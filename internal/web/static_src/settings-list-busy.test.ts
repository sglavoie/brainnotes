// @vitest-environment jsdom

import { beforeEach, describe, expect, it } from "vitest";

import { createSettingsPanel } from "./settings-controller";
import { jsonResponse } from "./shared.test-helpers";
import { listPayload, mount, templateSummary } from "./templates-controller.test-helpers";

beforeEach(() => {
  document.body.innerHTML = "";
});

type Gate = { promise: Promise<Response>; open: (response: Response) => void };
const gate = (): Gate => {
  let open: (response: Response) => void = () => {};
  const promise = new Promise<Response>((resolve) => {
    open = resolve;
  });
  return { promise, open };
};

const settingsList = {
  ok: true,
  generation: 7,
  action: "list",
  settings: [],
  config_diff: [],
  config: { generation: 7, source_hash: "h", status: "ok", restart_required: [], writable: true },
};

describe("list aria-busy while loading", () => {
  it("marks the settings list busy until the latest read answers", async () => {
    document.body.innerHTML = `
      <section data-settings-panel>
        <p data-settings-status></p>
        <ul data-settings-list aria-busy="false"></ul>
      </section>`;
    const gates = [gate(), gate()];
    let issued = 0;
    const panel = createSettingsPanel({
      root: document.querySelector("[data-settings-panel]") as HTMLElement,
      fetch: () => gates[issued++]?.promise ?? Promise.reject(new Error("extra read")),
      token: "csrf-token",
    });
    const list = document.querySelector("[data-settings-list]") as HTMLElement;
    const first = panel?.load();
    expect(list.getAttribute("aria-busy")).toBe("true");
    const second = panel?.load();
    gates[0]?.open(jsonResponse(settingsList));
    await first;
    expect(list.getAttribute("aria-busy")).toBe("true");
    gates[1]?.open(jsonResponse(settingsList));
    await second;
    expect(list.getAttribute("aria-busy")).toBe("false");
  });

  it("marks the templates list busy until the listing answers, even with an error", async () => {
    const replies = [gate(), gate()];
    let issued = 0;
    const mounted = mount({
      "/api/templates": () => replies[issued++]?.promise ?? Promise.reject(new Error("extra")),
    });
    const list = mounted.root.querySelector("[data-templates-list]") as HTMLElement;
    const loading = mounted.panel.load();
    expect(list.getAttribute("aria-busy")).toBe("true");
    replies[0]?.open(jsonResponse(listPayload([templateSummary()])));
    await loading;
    expect(list.getAttribute("aria-busy")).toBe("false");

    const failing = mounted.panel.load();
    expect(list.getAttribute("aria-busy")).toBe("true");
    replies[1]?.open(jsonResponse({ ok: false, message: "Unavailable." }, 503));
    await failing;
    expect(list.getAttribute("aria-busy")).toBe("false");
  });
});
