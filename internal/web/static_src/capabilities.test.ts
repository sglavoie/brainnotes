// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { noteNavigationDestination, normalizeOpenErrorMessage } from "./api";
import { initOpenInEditor } from "./editor-open";

afterEach(() => {
  vi.unstubAllGlobals();
  document.body.innerHTML = "";
});

const waitForExpect = async (assertion: () => void, attempts = 20): Promise<void> => {
  for (let i = 0; i < attempts; i += 1) {
    try {
      assertion();
      return;
    } catch {
      await new Promise((resolve) => setTimeout(resolve, 5));
    }
  }
  assertion();
};

describe("noteNavigationDestination", () => {
  it("reads the route a server without an editor answers with", () => {
    expect(noteNavigationDestination({ ok: true, destination: "/n/folder/note" })).toBe(
      "/n/folder/note"
    );
    expect(noteNavigationDestination({ ok: true, Destination: "/n/Note" })).toBe("/n/Note");
  });

  it("treats an absent destination as an editor launch rather than an error", () => {
    expect(noteNavigationDestination({ ok: true })).toBe("");
    expect(noteNavigationDestination("")).toBe("");
    expect(noteNavigationDestination(null)).toBe("");
    expect(noteNavigationDestination(undefined)).toBe("");
  });

  // The value is handed to window.location.assign, so anything but a note route
  // would turn an Open button into an open redirect.
  it("refuses a destination that is not a note route", () => {
    for (const destination of [
      "https://evil.example/n/Note",
      "//evil.example/n/Note",
      "/media/secret.txt",
      "javascript:alert(1)",
      "n/Note",
      "/nope/Note",
    ]) {
      expect(noteNavigationDestination({ ok: true, destination })).toBe("");
    }
  });
});

describe("normalizeOpenErrorMessage", () => {
  it("reads the guard's management envelope as well as a handler's plain text", () => {
    expect(
      normalizeOpenErrorMessage(403, {
        ok: false,
        code: "forbidden",
        message: "process launch is unavailable in hosted mode",
      })
    ).toBe("process launch is unavailable in hosted mode");
    expect(normalizeOpenErrorMessage(403, "csrf token required")).toBe("csrf token required");
    expect(normalizeOpenErrorMessage(500, "")).toBe("Open failed (500).");
    expect(normalizeOpenErrorMessage(500, { ok: false })).toBe("Open failed (500).");
  });
});

describe("open-in-editor under a server that cannot launch a process", () => {
  const renderOpenControl = (): void => {
    document.body.innerHTML = `
      <div data-open-editor data-open-key="folder/note.md" data-open-csrf="csrf-token">
        <button type="button" data-open-button>Open in editor</button>
        <p data-open-status> </p>
      </div>
    `;
  };

  it("navigates to the answered destination instead of claiming an editor opened", async () => {
    const assign = vi.fn();
    vi.stubGlobal("window", { ...window, location: { assign } });
    renderOpenControl();
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => ({
        ok: true,
        headers: { get: () => "application/json" },
        json: async () => ({ ok: true, destination: "/n/folder/note" }),
      }))
    );

    initOpenInEditor();
    document.querySelector<HTMLButtonElement>("[data-open-button]")?.click();

    await waitForExpect(() => {
      expect(assign).toHaveBeenCalledWith("/n/folder/note");
    });
    const status = document.querySelector<HTMLElement>("[data-open-status]");
    expect(status?.textContent).not.toContain("Opened in editor");
  });

  it("reports the guard's refusal rather than a bare status code", async () => {
    renderOpenControl();
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => ({
        ok: false,
        status: 403,
        headers: { get: () => "application/json" },
        json: async () => ({
          ok: false,
          code: "forbidden",
          message: "process launch is unavailable in hosted mode",
        }),
      }))
    );

    initOpenInEditor();
    document.querySelector<HTMLButtonElement>("[data-open-button]")?.click();

    const status = document.querySelector<HTMLElement>("[data-open-status]");
    await waitForExpect(() => {
      expect(status?.textContent).toBe("process launch is unavailable in hosted mode");
    });
    expect(status?.classList.contains("is-error")).toBe(true);
  });
});
