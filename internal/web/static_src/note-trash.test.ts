// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import {
  buildTrashNoteFetchRequest,
  initTrashNoteAction,
  readTrashNoteConfig,
} from "./note-trash";

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

const trashResponse = (body: unknown, status = 200): Response =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

afterEach(() => {
  document.body.innerHTML = "";
});

describe("trash note request construction", () => {
  it("reads note config and builds the trash request form", () => {
    document.body.innerHTML = `
      <div
        data-trash-note
        data-trash-key="folder/note.md"
        data-trash-csrf="csrf-token"
        data-trash-confirm="Move this note?"
        data-trash-redirect="/"
      ></div>
    `;
    const container = document.querySelector<HTMLElement>("[data-trash-note]");
    expect(container).not.toBeNull();

    const config = readTrashNoteConfig(container!);
    const request = buildTrashNoteFetchRequest(config);
    const headers = request.init.headers as Record<string, string>;

    expect(config).toEqual({
      key: "folder/note.md",
      csrfToken: "csrf-token",
      confirmMessage: "Move this note?",
      redirectPath: "/",
    });
    expect(request.url).toBe("/api/notes/trash");
    expect(request.init.method).toBe("POST");
    expect(headers.Accept).toBe("application/json");
    expect(headers["Content-Type"]).toBe(
      "application/x-www-form-urlencoded; charset=UTF-8"
    );
    expect(headers["X-BN-CSRF"]).toBe("csrf-token");
    expect(request.init.body).toBe("key=folder%2Fnote.md&token=csrf-token");
  });
});

describe("trash note DOM behavior", () => {
  it("cancels without posting when confirmation is rejected", async () => {
    document.body.innerHTML = `
      <div data-trash-note data-trash-key="source" data-trash-csrf="csrf-token" data-trash-confirm="Move Source?" data-trash-redirect="/">
        <button type="button" data-trash-button>Trash</button>
        <p data-trash-status> </p>
      </div>
    `;
    const fetchMock = vi.fn(async () => trashResponse({ ok: true }));
    const confirmMock = vi.fn(() => false);
    const redirectMock = vi.fn();

    initTrashNoteAction({
      fetch: fetchMock as typeof fetch,
      confirm: confirmMock,
      redirect: redirectMock,
    });
    document.querySelector<HTMLButtonElement>("[data-trash-button]")?.click();

    const status = document.querySelector<HTMLElement>("[data-trash-status]");
    expect(confirmMock).toHaveBeenCalledWith("Move Source?");
    expect(fetchMock).not.toHaveBeenCalled();
    expect(redirectMock).not.toHaveBeenCalled();
    await waitForExpect(() => expect(status?.textContent).toBe("Trash canceled."));
  });

  it("posts the configured request and redirects on success", async () => {
    document.body.innerHTML = `
      <div data-trash-note data-trash-key="folder/note.md" data-trash-csrf="csrf-token" data-trash-confirm="Move Note?" data-trash-redirect="/">
        <button type="button" data-trash-button>Trash</button>
        <p data-trash-status> </p>
      </div>
    `;
    const fetchMock = vi.fn(async () =>
      trashResponse({ ok: true, message: "Moved note to Trash." })
    );
    const redirectMock = vi.fn();

    initTrashNoteAction({
      fetch: fetchMock as typeof fetch,
      confirm: () => true,
      redirect: redirectMock,
    });
    const button = document.querySelector<HTMLButtonElement>("[data-trash-button]");
    const status = document.querySelector<HTMLElement>("[data-trash-status]");
    button?.click();

    expect(button?.disabled).toBe(true);

    await waitForExpect(() => {
      expect(button?.disabled).toBe(false);
    });

    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/notes/trash");
    expect(init).toMatchObject({
      method: "POST",
    });
    const body = new URLSearchParams(String(init.body));
    expect(body.get("key")).toBe("folder/note.md");
    expect(body.get("token")).toBe("csrf-token");
    expect(body.get("operation_id")).toMatch(/^[0-9a-f]{32}$/);
    expect(status?.textContent).toBe("Moved note to Trash. Redirecting…");
    expect(status?.classList.contains("is-success")).toBe(true);
    expect(redirectMock).toHaveBeenCalledWith("/");
  });

  it("shows server error messages without redirecting", async () => {
    document.body.innerHTML = `
      <div data-trash-note data-trash-key="source" data-trash-csrf="csrf-token" data-trash-confirm="Move Source?" data-trash-redirect="/">
        <button type="button" data-trash-button>Trash</button>
        <p data-trash-status> </p>
      </div>
    `;
    const fetchMock = vi.fn(async () =>
      trashResponse({ ok: false, code: "conflict", message: "note is missing" }, 409)
    );
    const redirectMock = vi.fn();

    initTrashNoteAction({
      fetch: fetchMock as typeof fetch,
      confirm: () => true,
      redirect: redirectMock,
    });
    const button = document.querySelector<HTMLButtonElement>("[data-trash-button]");
    const status = document.querySelector<HTMLElement>("[data-trash-status]");
    button?.click();

    await waitForExpect(() => {
      expect(button?.disabled).toBe(false);
    });

    expect(status?.textContent).toBe("note is missing");
    expect(status?.classList.contains("is-error")).toBe(true);
    expect(redirectMock).not.toHaveBeenCalled();
  });
});
