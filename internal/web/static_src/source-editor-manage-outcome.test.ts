// @vitest-environment jsdom


import { beforeEach, describe, expect, it, vi } from "vitest";

import { editorURLForKey, readManageOutcome } from "./source-manage-client";
import { initSourceManage } from "./source-editor-manage";
import { deferred, harness, jsonResponse, config, manageMarkup } from "./source-editor-manage.test-helpers";

beforeEach(() => {
  document.body.innerHTML = manageMarkup;
});

describe("readManageOutcome", () => {
  it("carries the key and path the operation ended at", () => {
    const outcome = readManageOutcome(200, true, {
      ok: true,
      message: "Renamed.",
      key: "notes/beta",
      rel_path: "notes/Beta.md",
    });
    expect(outcome).toMatchObject({
      ok: true,
      key: "notes/beta",
      relPath: "notes/Beta.md",
      message: "Renamed.",
    });
  });

  it("reads an ok:false body served with a 200 as a refusal", () => {
    const outcome = readManageOutcome(200, true, { ok: false, message: "No." });
    expect(outcome.ok).toBe(false);
    expect(outcome.message).toBe("No.");
  });

  it("falls back to the status when the body explains nothing", () => {
    expect(readManageOutcome(500, false, null).message).toBe("Request failed (500).");
  });

  it("defaults a success with no message to something a person can read", () => {
    expect(readManageOutcome(200, true, { ok: true }).message).toBe("Done.");
  });
});

// There is no field a response could carry that would turn a rename into a
// redirect somewhere else: the route comes from the page and the key is encoded.
describe("editorURLForKey", () => {
  it("encodes the key into the page's own editor route", () => {
    expect(editorURLForKey("/edit", "a/b c.md")).toBe("/edit?key=a%2Fb%20c.md");
  });

  it("cannot be talked into another origin", () => {
    expect(editorURLForKey("/edit", "https://evil.example/")).toBe(
      "/edit?key=https%3A%2F%2Fevil.example%2F"
    );
  });
});

describe("editor management confirmation", () => {
  it.each([null, {}, [], "Done", { ok: "true" }])("does not confirm a malformed success: %j", (payload) => {
    expect(readManageOutcome(200, true, payload)).toMatchObject({ ok: false, unconfirmed: true });
  });

  it.each(["lost", "invalid JSON", "missing confirmation"])("keeps a capture after %s", async (failure) => {
    const h = harness();
    if (failure === "lost") h.fetcher.mockRejectedValue(new Error("response lost"));
    else if (failure === "invalid JSON") h.fetcher.mockResolvedValue(new Response("{", {
      headers: { "Content-Type": "application/json" },
    }));
    else h.fetcher.mockResolvedValue(jsonResponse(200, {}));
    h.type("[data-source-capture-text]", "Keep this thought");
    h.click("[data-source-capture]");
    await vi.waitFor(() => expect(h.status().text).toContain("Retry safely with the same text"));
    expect(h.elements.captureText?.value).toBe("Keep this thought");
    expect(h.elements.capture?.disabled).toBe(false);
    expect(h.fetcher).toHaveBeenCalledOnce();
    expect(h.reloads).toBe(0);
  });

  it("keeps the create target when creation is unconfirmed", async () => {
    const h = harness();
    h.fetcher.mockResolvedValue(jsonResponse(200, null));
    h.type("[data-source-note-create-target]", "New thought");
    h.click("[data-source-note-create]");
    await vi.waitFor(() => expect(h.status().text).toContain("not confirmed"));
    expect(h.elements.createTarget?.value).toBe("New thought");
  });

  it.each(["rename", "move"])("does not navigate after %s without a confirmed destination", async (action) => {
    const h = harness();
    h.type("[data-source-rename-title]", "Beta");
    h.type("[data-source-move-folder]", "Notes");
    h.click(`[data-source-${action}]`);
    await vi.waitFor(() => expect(h.status().text).toContain("not confirmed"));
    expect(h.navigated).toEqual([]);
  });
});

describe("editor management pending state", () => {
  it("blocks repeated and competing actions until the pending response is consumed", async () => {
    const h = harness();
    const response = deferred<Response>();
    h.fetcher.mockReturnValue(response.promise);
    h.type("[data-source-capture-text]", "One thought");
    h.click("[data-source-capture]");
    expect(h.elements.capture?.disabled).toBe(true);
    expect(h.elements.rename?.disabled).toBe(true);
    expect(h.elements.undo?.disabled).toBe(true);
    h.click("[data-source-capture]");
    h.elements.undo?.dispatchEvent(new MouseEvent("click"));
    expect(h.fetcher).toHaveBeenCalledOnce();
    response.resolve(jsonResponse(200, { ok: true, detail: "- One thought" }));
    await vi.waitFor(() => expect(h.elements.capture?.disabled).toBe(false));
    expect(h.elements.rename?.disabled).toBe(false);
    expect(h.elements.captureText?.value).toBe("");
    expect(h.fetcher).toHaveBeenCalledOnce();
  });

  it("restores prior disabled states after a refusal", async () => {
    const h = harness();
    h.elements.undo!.disabled = true;
    h.fetcher.mockResolvedValue(jsonResponse(409, { ok: false, message: "Conflict." }));
    h.click("[data-source-pin]");
    await vi.waitFor(() => expect(h.status().text).toBe("Conflict."));
    expect(h.elements.pin?.disabled).toBe(false);
    expect(h.elements.undo?.disabled).toBe(true);
  });

  it("blocks duplicate saves and cancels the continuation while saving", async () => {
    const h = harness({ dirty: true });
    const saved = deferred<void>();
    h.host.save = vi.fn().mockReturnValue(saved.promise);
    // initSourceManage captures host.save, so bind again with this delayed host.
    h.dispose();
    const dispose = initSourceManage({ elements: h.elements, config, host: h.host, fetch: h.fetcher as unknown as typeof fetch });
    h.type("[data-source-rename-title]", "Beta");
    h.click("[data-source-rename]");
    h.elements.gate.save?.click();
    h.elements.gate.save?.dispatchEvent(new MouseEvent("click"));
    h.click("[data-source-pin]");
    expect(h.host.save).toHaveBeenCalledOnce();
    expect(h.elements.pin?.disabled).toBe(true);
    h.elements.gate.cancel?.click();
    expect(h.status().text).toContain("The save is still in progress; this action will not run.");
    h.setDirty(false);
    saved.resolve();
    await vi.waitFor(() => expect(h.elements.pin?.disabled).toBe(false));
    expect(h.fetcher).not.toHaveBeenCalled();
    dispose();
  });
});

describe("editor management local feedback", () => {
  it("keeps loading, error, and success beside the control that initiated them", async () => {
    const h = harness();
    const group = document.createElement("div");
    group.dataset.sourceManageFeedback = "capture";
    const status = document.createElement("p");
    status.dataset.sourceManageStatus = "";
    h.elements.root.append(group);
    group.append(h.elements.capture!, status);
    const response = deferred<Response>();
    h.fetcher.mockReturnValueOnce(response.promise);
    h.type("[data-source-capture-text]", "Local feedback");
    h.click("[data-source-capture]");
    expect(status.textContent).toBe("Capture this line…");
    expect(h.status().text).toBe("");
    response.reject(new Error("lost"));
    await vi.waitFor(() => expect(status.textContent).toContain("not confirmed"));
    h.fetcher.mockResolvedValueOnce(jsonResponse(200, { ok: true, message: "Captured." }));
    h.click("[data-source-capture]");
    await vi.waitFor(() => expect(status.textContent).toBe("Captured."));
    expect(h.status().text).toBe("");
  });
});
