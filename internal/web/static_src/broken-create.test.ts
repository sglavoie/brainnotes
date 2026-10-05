// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import {
  buildCreateNoteFetchRequest,
  initBrokenCreateActions,
  readCreateNoteConfig,
} from "./broken-create";

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

const createResponse = (body: unknown, status = 200): Response =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

afterEach(() => {
  document.body.innerHTML = "";
});

describe("broken create request construction", () => {
  it("reads config and builds the create request form", () => {
    document.body.innerHTML = `
      <div
        data-create-note
        data-create-source-key="folder/source"
        data-create-target="Missing Note"
        data-create-csrf="csrf-token"
      ></div>
    `;
    const container = document.querySelector<HTMLElement>("[data-create-note]");
    expect(container).not.toBeNull();

    const config = readCreateNoteConfig(container!);
    const request = buildCreateNoteFetchRequest(config);
    const headers = request.init.headers as Record<string, string>;

    expect(config).toEqual({
      sourceKey: "folder/source",
      target: "Missing Note",
      csrfToken: "csrf-token",
    });
    expect(request.url).toBe("/api/notes/create");
    expect(request.init.method).toBe("POST");
    expect(headers.Accept).toBe("application/json");
    expect(headers["Content-Type"]).toBe(
      "application/x-www-form-urlencoded; charset=UTF-8"
    );
    expect(headers["X-BN-CSRF"]).toBe("csrf-token");
    expect(request.init.body).toBe(
      "key=folder%2Fsource&token=csrf-token&target=Missing+Note"
    );
  });
});

describe("broken create DOM behavior", () => {
  it("posts the configured request and replaces Create with the new note link", async () => {
    document.body.innerHTML = `
      <div data-create-note data-create-source-key="folder/source" data-create-target="Missing Note" data-create-csrf="csrf-token">
        <button type="button" data-create-button>Create note</button>
        <span data-create-status> </span>
      </div>
    `;
    const fetchMock = vi.fn(async () =>
      createResponse({ ok: true, message: "Created note.", path: "/n/folder/missing%20note" })
    );

    initBrokenCreateActions({ fetch: fetchMock as typeof fetch });
    const button = document.querySelector<HTMLButtonElement>("[data-create-button]");
    const status = document.querySelector<HTMLElement>("[data-create-status]");
    button?.click();

    expect(button?.disabled).toBe(true);
    expect(status?.textContent).toBe("Creating note...");

    await waitForExpect(() => {
      expect(document.querySelector("[data-create-note] a")?.textContent).toBe("Open note");
    });

    expect(document.querySelector("[data-create-note] a")?.getAttribute("href")).toBe("/n/folder/missing%20note");
    expect(button?.isConnected).toBe(false);
    expect(button?.disabled).toBe(true);
    button?.click();
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/notes/create");
    expect(init).toMatchObject({
      method: "POST",
      body: "key=folder%2Fsource&token=csrf-token&target=Missing+Note",
    });
    expect(status?.textContent).toBe("Created note.");
    expect(status?.classList.contains("is-success")).toBe(true);
  });

  it("shows server error messages", async () => {
    document.body.innerHTML = `
      <div data-create-note data-create-source-key="source" data-create-target="Missing" data-create-csrf="csrf-token">
        <button type="button" data-create-button>Create note</button>
        <span data-create-status> </span>
      </div>
    `;
    const fetchMock = vi.fn(async () =>
      createResponse({ ok: false, code: "conflict", message: "destination already exists" }, 409)
    );

    initBrokenCreateActions({ fetch: fetchMock as typeof fetch });
    const button = document.querySelector<HTMLButtonElement>("[data-create-button]");
    const status = document.querySelector<HTMLElement>("[data-create-status]");
    button?.click();

    await waitForExpect(() => {
      expect(button?.disabled).toBe(false);
    });

    expect(status?.textContent).toBe("destination already exists");
    expect(status?.classList.contains("is-error")).toBe(true);
  });
});
