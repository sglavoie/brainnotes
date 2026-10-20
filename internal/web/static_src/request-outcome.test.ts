import { applySyncResolution } from "./sync-requests";
import { requestSynthesisApply } from "./api-synthesis";
import { describe, expect, it } from "vitest";
import { requestErrorMessage, responseFailure, serverUnreachableMessage, unansweredRequest } from "./request-outcome";
import { applySetting, applyTemplate, fetchSettings, previewSetting } from "./settings-requests";
import { applySavedView } from "./saved-views-requests";
import { applyPropertyMutation, undoJournalEntry } from "./properties-workbench-requests";
import { restoreHistoryEntry } from "./history-requests";

describe("uncertain writes", () => {
  const writers = [
    ["sync", (fetcher: typeof fetch) => applySyncResolution(fetcher, "", "csrf", "receipt")],
    ["setting", (fetcher: typeof fetch) => applySetting(fetcher, "csrf", "receipt")],
    ["template", (fetcher: typeof fetch) => applyTemplate(fetcher, "csrf", "receipt")],
    ["view", (fetcher: typeof fetch) => applySavedView(fetcher, "csrf", "receipt")],
    ["property", (fetcher: typeof fetch) => applyPropertyMutation(fetcher, "csrf", "receipt")],
    ["undo", (fetcher: typeof fetch) => undoJournalEntry(fetcher, "csrf", "entry")],
    ["restore", (fetcher: typeof fetch) => restoreHistoryEntry(fetcher, "/api/undo", { token: "csrf", entryID: "entry" })],
  ] as const;
  it.each(writers)("%s never claims success or refusal for an unreadable reply", async (_, write) => {
    for (const body of ["<html>Sign in</html>", '{"ok":', '{}', '[]']) {
      const result = await write(async () => new Response(body));
      expect(result.ok).toBe(false);
      expect(result).toHaveProperty("message", unansweredRequest("write").message);
    }
  });
  it.each(writers)("%s reports a dropped response as unconfirmed", async (_, write) => {
    const result = await write(async () => { throw new TypeError("lost response"); });
    expect(result).toHaveProperty("message", unansweredRequest("write").message);
  });
  it("synthesis keeps an uncertain creation distinct from a refusal", async () => {
    await expect(requestSynthesisApply(async () => new Response("<html>Sign in</html>"), "", { token: "csrf", preview_id: "receipt" }))
      .rejects.toMatchObject({ code: "unconfirmed", message: unansweredRequest("write").message });
  });
  it("does not trust a redirected JSON success", () => {
    const response = new Response('{"ok":true}');
    Object.defineProperty(response, "redirected", { value: true });
    expect(responseFailure(response, { ok: true }, "write")).toEqual(unansweredRequest("write"));
  });
  it("retains the server's explicit refusal", () => {
    const payload = { ok: false, code: "preview_stale", message: "The file changed. Preview again." };
    expect(responseFailure(new Response("", { status: 409 }), payload, "write"))
      .toEqual({ ok: false, message: payload.message });
  });
  it("explains that a failed preview submitted no change", async () => {
    const fetcher = async (): Promise<Response> => { throw new TypeError("offline"); };
    expect(await fetchSettings(fetcher)).toEqual(unansweredRequest("read"));
    expect(await previewSetting(fetcher, "csrf", { token: "csrf", op: "unset", key: "capture.heading" }))
      .toEqual(unansweredRequest("preview"));
  });
});

describe("requestErrorMessage", () => {
  it("never shows the browser's own network failure wording", () => {
    for (const wording of ["Failed to fetch", "Load failed", "NetworkError when attempting to fetch resource."]) {
      expect(requestErrorMessage(new TypeError(wording), "fallback")).toBe(serverUnreachableMessage);
    }
  });
  it("keeps a server's own message and falls back when there is none", () => {
    expect(requestErrorMessage(new Error(" Note not found. "), "fallback")).toBe("Note not found.");
    expect(requestErrorMessage("Rendering refused.", "fallback")).toBe("Rendering refused.");
    expect(requestErrorMessage(new Error(""), "fallback")).toBe("fallback");
    expect(requestErrorMessage(null, "fallback")).toBe("fallback");
  });
});
