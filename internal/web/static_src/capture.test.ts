// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import { buildCaptureForm, managementMutationEventName } from "./api";
import { initCapture } from "./capture";
import { jsonResponse, waitForExpect } from "./shared.test-helpers";

const renderPanel = (configured = "true"): void => {
  document.body.innerHTML = `
    <section
      data-capture-root
      data-capture-csrf="csrf-token"
      data-capture-target="Inbox.md"
      ${configured === "" ? "" : `data-capture-configured="${configured}"`}
    >
      <textarea data-capture-input rows="2"></textarea>
      <button type="button" data-capture-submit>Capture</button>
      <p data-capture-status role="status" aria-live="polite"></p>
      <ul data-capture-recent></ul>
    </section>
  `;
};

const elements = () => ({
  input: document.querySelector<HTMLTextAreaElement>("[data-capture-input]")!,
  submit: document.querySelector<HTMLButtonElement>("[data-capture-submit]")!,
  status: document.querySelector<HTMLElement>("[data-capture-status]")!,
  recent: document.querySelector<HTMLElement>("[data-capture-recent]")!,
});

afterEach(() => {
  document.body.innerHTML = "";
});

describe("capture request construction", () => {
  it("builds the urlencoded capture form", () => {
    const body = buildCaptureForm({ text: "call [[Bob]] back", token: "csrf-token" });
    expect(body.toString()).toBe("text=call+%5B%5BBob%5D%5D+back&token=csrf-token");
  });
});

describe("capture focus", () => {
  it.each(["success", "refused", "offline"])("preserves outside focus after delayed %s and cleans up tracking", async (outcome) => {
    renderPanel();
    const search = document.createElement("input");
    document.body.append(search);
    let finish!: (response: Response) => void;
    let fail!: (error: Error) => void;
    const pending = new Promise<Response>((resolve, reject) => { finish = resolve; fail = reject; });
    const fetcher = vi.fn().mockReturnValueOnce(pending).mockResolvedValue(jsonResponse({ ok: true }));
    initCapture({ fetch: fetcher });
    const { input, submit, status } = elements();
    input.value = "Keep this thought";
    input.focus();
    submit.click();
    search.focus();
    if (outcome === "offline") fail(new Error("offline"));
    else finish(jsonResponse({ ok: outcome === "success" }, outcome === "success" ? 200 : 409));
    await waitForExpect(() => expect(submit.disabled).toBe(false));
    expect(document.activeElement).toBe(search);
    expect(status.classList.contains(outcome === "success" ? "is-success" : "is-error")).toBe(true);
    if (outcome !== "success") expect(input.value).toBe("Keep this thought");

    // Interaction between requests must not cancel the next request's return.
    search.dispatchEvent(new Event("pointerdown", { bubbles: true }));
    input.value = "Next thought";
    input.focus();
    submit.click();
    await waitForExpect(() => expect(submit.disabled).toBe(false));
    expect(document.activeElement).toBe(input);
  });

  it("does not return focus after a pointer gesture on non-focusable content", async () => {
    renderPanel();
    let finish!: (response: Response) => void;
    initCapture({ fetch: vi.fn(() => new Promise<Response>((resolve) => { finish = resolve; })) });
    const { input, submit, status } = elements();
    input.value = "Thought";
    input.focus();
    submit.click();
    const focus = vi.spyOn(input, "focus");
    status.dispatchEvent(new Event("pointerdown", { bubbles: true }));
    finish(jsonResponse({ ok: true }));
    await waitForExpect(() => expect(submit.disabled).toBe(false));
    expect(focus).not.toHaveBeenCalled();
  });

  it("returns focus after a delayed failure when the user stays in capture", async () => {
    renderPanel();
    let finish!: (response: Response) => void;
    initCapture({ fetch: vi.fn(() => new Promise<Response>((resolve) => { finish = resolve; })) });
    const { input, submit } = elements();
    input.value = "Keep this thought";
    input.focus();
    submit.click();
    finish(jsonResponse({ ok: false }, 409));
    await waitForExpect(() => expect(submit.disabled).toBe(false));
    expect(document.activeElement).toBe(input);
    expect(input.value).toBe("Keep this thought");
  });

});

describe("capture success", () => {
  it("posts the capture, clears the box, and echoes the server bullet", async () => {
    renderPanel();
    const fetchMock = vi.fn(async () =>
      jsonResponse({
        ok: true,
        message: "Captured to Inbox.md.",
        detail: "- 2026-08-17 14:03 call [[Bob]] back",
        rel_path: "Inbox.md",
      })
    );
    const mutationListener = vi.fn();
    window.addEventListener(managementMutationEventName, mutationListener, { once: true });

    initCapture({ fetch: fetchMock as typeof fetch });
    const { input, submit, status, recent } = elements();
    input.value = "call [[Bob]] back";
    submit.click();

    await waitForExpect(() => {
      expect(submit.disabled).toBe(false);
    });

    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    const headers = init.headers as Record<string, string>;
    expect(url).toBe("/api/capture");
    expect(init.method).toBe("POST");
    expect(headers["X-BN-CSRF"]).toBe("csrf-token");
    expect(headers["Content-Type"]).toBe(
      "application/x-www-form-urlencoded; charset=UTF-8"
    );
    expect(new URLSearchParams(String(init.body)).get("text")).toBe("call [[Bob]] back");
    expect(new URLSearchParams(String(init.body)).get("capture_id")).toBeTruthy();

    expect(input.value).toBe("");
    expect(document.activeElement).toBe(input);
    expect(recent.children).toHaveLength(1);
    expect(recent.firstElementChild?.textContent).toBe("- 2026-08-17 14:03 call [[Bob]] back");
    expect(status.textContent).toBe("Captured to Inbox.md.");
    expect(status.classList.contains("is-success")).toBe(true);
    expect(mutationListener).toHaveBeenCalledTimes(1);
  });

  it("prepends each new bullet so the newest capture is first", async () => {
    renderPanel();
    let counter = 0;
    const fetchMock = vi.fn(async () => {
      counter += 1;
      return jsonResponse({ ok: true, message: "Captured.", detail: `- bullet ${counter}` });
    });

    initCapture({ fetch: fetchMock as typeof fetch });
    const { input, submit, recent } = elements();

    input.value = "first";
    submit.click();
    await waitForExpect(() => {
      expect(recent.children).toHaveLength(1);
    });
    input.value = "second";
    submit.click();
    await waitForExpect(() => {
      expect(recent.children).toHaveLength(2);
    });

    expect(Array.from(recent.children).map((item) => item.textContent)).toEqual([
      "- bullet 2",
      "- bullet 1",
    ]);
  });

});

describe("capture errors and keyboard", () => {
  it.each(["lost response", "unreadable response"])("preserves the draft and offers the inbox after an %s", async (outcome) => {
    renderPanel();
    document.querySelector("[data-capture-root]")!.insertAdjacentHTML("beforeend",
      '<a data-capture-view-inbox href="/n/inbox" hidden>View inbox</a>');
    const fetcher = vi.fn(async () => {
      if (outcome === "lost response") throw new Error("offline");
      return new Response("{", { headers: { "Content-Type": "application/json" } });
    });
    initCapture({ fetch: fetcher });
    const { input, submit, status, recent } = elements();
    input.value = "Possibly already captured";
    submit.click();
    await waitForExpect(() => expect(submit.disabled).toBe(false));
    expect(input.value).toBe("Possibly already captured");
    expect(status.textContent).toBe("Capture not confirmed. Retry safely with the same text; a recorded attempt will not be added again.");
    expect(document.querySelector<HTMLAnchorElement>("[data-capture-view-inbox]")!.hidden).toBe(false);
    expect(recent.children).toHaveLength(0);
    expect(fetcher).toHaveBeenCalledOnce();
  });

  it("keeps the typed text and reports the server error", async () => {
    renderPanel();
    const fetchMock = vi.fn(async () =>
      jsonResponse({ ok: false, code: "conflict", message: "capture: note is being edited" }, 409)
    );

    initCapture({ fetch: fetchMock as typeof fetch });
    const { input, submit, status, recent } = elements();
    input.value = "keep me";
    submit.click();

    await waitForExpect(() => {
      expect(submit.disabled).toBe(false);
    });

    expect(input.value).toBe("keep me");
    expect(recent.children).toHaveLength(0);
    expect(status.textContent).toBe("capture: note is being edited");
    expect(status.classList.contains("is-error")).toBe(true);
  });

  it("refuses an all-blank capture without calling the endpoint", async () => {
    renderPanel();
    const fetchMock = vi.fn(async () => jsonResponse({ ok: true }));

    initCapture({ fetch: fetchMock as typeof fetch });
    const { input, submit, status } = elements();
    input.value = "   \n  ";
    submit.click();

    await waitForExpect(() => {
      expect(status.textContent).toBe("Type something to capture.");
    });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("submits on Ctrl+Enter", async () => {
    renderPanel();
    const fetchMock = vi.fn(async () =>
      jsonResponse({ ok: true, message: "Captured to Inbox.md.", detail: "- bullet" })
    );

    initCapture({ fetch: fetchMock as typeof fetch });
    const { input, submit } = elements();
    input.value = "keyboard capture";
    input.dispatchEvent(
      new KeyboardEvent("keydown", { bubbles: true, cancelable: true, ctrlKey: true, key: "Enter" })
    );

    await waitForExpect(() => {
      expect(submit.disabled).toBe(false);
    });
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(input.value).toBe("");
  });

  it("ignores a plain Enter so newlines still work", () => {
    renderPanel();
    const fetchMock = vi.fn(async () => jsonResponse({ ok: true }));

    initCapture({ fetch: fetchMock as typeof fetch });
    const { input } = elements();
    input.value = "line one";
    input.dispatchEvent(
      new KeyboardEvent("keydown", { bubbles: true, cancelable: true, key: "Enter" })
    );

    expect(fetchMock).not.toHaveBeenCalled();
  });

});

describe("capture configuration", () => {
  it("disables the box when data-capture-configured is absent", () => {
    renderPanel("");
    const fetchMock = vi.fn(async () => jsonResponse({ ok: true }));

    initCapture({ fetch: fetchMock as typeof fetch });
    const { input, submit, status } = elements();

    expect(input.disabled).toBe(true);
    expect(submit.disabled).toBe(true);
    expect(status.classList.contains("is-error")).toBe(true);
    expect(status.textContent).toContain("not configured");

    submit.click();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("disables the box when data-capture-configured is false", () => {
    renderPanel("false");
    const fetchMock = vi.fn(async () => jsonResponse({ ok: true }));

    initCapture({ fetch: fetchMock as typeof fetch });
    const { input, submit } = elements();

    expect(input.disabled).toBe(true);
    expect(submit.disabled).toBe(true);
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
