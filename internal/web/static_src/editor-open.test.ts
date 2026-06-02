// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import {
  buildOpenEditorFetchRequest,
  initOpenInEditor,
  readOpenEditorConfig,
} from "./editor-open";

const waitForExpect = async (assertion: () => void): Promise<void> => {
  let lastError: unknown;
  for (let attempt = 0; attempt < 20; attempt += 1) {
    try {
      assertion();
      return;
    } catch (error) {
      lastError = error;
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
  }
  throw lastError;
};

afterEach(() => {
  vi.unstubAllGlobals();
  document.body.innerHTML = "";
});

describe("open-in-editor request construction", () => {
  it("reads note config and builds the open request form", () => {
    document.body.innerHTML = `
      <div data-open-editor data-open-key="folder/note.md" data-open-csrf="csrf-token"></div>
    `;
    const container = document.querySelector<HTMLElement>("[data-open-editor]");
    expect(container).not.toBeNull();

    const config = readOpenEditorConfig(container!);
    const request = buildOpenEditorFetchRequest(config);
    const headers = request.init.headers as Record<string, string>;

    expect(config).toEqual({
      key: "folder/note.md",
      csrfToken: "csrf-token",
    });
    expect(request.url).toBe("/api/open");
    expect(request.init.method).toBe("POST");
    expect(headers.Accept).toBe("application/json");
    expect(headers["Content-Type"]).toBe(
      "application/x-www-form-urlencoded; charset=UTF-8"
    );
    expect(headers["X-BN-CSRF"]).toBe("csrf-token");
    expect(request.init.body).toBe("key=folder%2Fnote.md&token=csrf-token");
  });
});

describe("open-in-editor DOM behavior", () => {
  it("posts the configured request and restores button state on success", async () => {
    document.body.innerHTML = `
      <div data-open-editor data-open-key="folder/note.md" data-open-csrf="csrf-token">
        <button type="button" data-open-button>Open in editor</button>
        <p data-open-status> </p>
      </div>
    `;
    const fetchMock = vi.fn(async () => ({
      ok: true,
      text: async () => "",
    }));
    vi.stubGlobal("fetch", fetchMock);

    initOpenInEditor();
    const button = document.querySelector<HTMLButtonElement>("[data-open-button]");
    const status = document.querySelector<HTMLElement>("[data-open-status]");
    button?.click();

    expect(button?.disabled).toBe(true);
    expect(status?.textContent).toContain("Opening editor");

    await waitForExpect(() => {
      expect(button?.disabled).toBe(false);
    });

    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/open");
    expect(init).toMatchObject({
      method: "POST",
      body: "key=folder%2Fnote.md&token=csrf-token",
    });
    expect(status?.textContent).toBe("Opened in editor. Refresh after saving changes.");
    expect(status?.classList.contains("is-success")).toBe(true);
  });

  it("does not send an open request when config is incomplete", () => {
    document.body.innerHTML = `
      <div data-open-editor data-open-key="" data-open-csrf="">
        <button type="button" data-open-button>Open in editor</button>
        <p data-open-status> </p>
      </div>
    `;
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    initOpenInEditor();
    document.querySelector<HTMLButtonElement>("[data-open-button]")?.click();

    const status = document.querySelector<HTMLElement>("[data-open-status]");
    expect(fetchMock).not.toHaveBeenCalled();
    expect(status?.textContent).toBe("Open action unavailable.");
    expect(status?.classList.contains("is-error")).toBe(true);
  });
});
