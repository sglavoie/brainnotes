// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import {
  buildNotePinToggleRequest,
  initNotePinAction,
  readNotePinConfig,
} from "./note-pin";

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

const pinResponse = (body: unknown, status = 200): Response =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

const renderNotePinFixture = (state: "pinned" | "unpinned"): void => {
  document.body.innerHTML = `
    <div data-pin-note data-pin-key="folder/note.md" data-pin-csrf="csrf-token" data-pin-state="${state}">
      <button type="button" class="pin-button" data-pin-button aria-pressed="${state === "pinned"}">${state === "pinned" ? "Unpin" : "Pin"}</button>
      <p data-pin-status> </p>
    </div>
  `;
};

afterEach(() => {
  document.body.innerHTML = "";
});

describe("note pin request construction", () => {
  it("reads the group config and posts to the set route when unpinned", () => {
    renderNotePinFixture("unpinned");
    const container = document.querySelector<HTMLElement>("[data-pin-note]");
    const config = readNotePinConfig(container!);

    expect(config).toEqual({
      key: "folder/note.md",
      csrfToken: "csrf-token",
      pinned: false,
    });

    const request = buildNotePinToggleRequest(config);
    const headers = request.init.headers as Record<string, string>;
    expect(request.url).toBe("/api/pins/set");
    expect(request.init.method).toBe("POST");
    expect(headers["X-BN-CSRF"]).toBe("csrf-token");
    expect(request.init.body).toBe("key=folder%2Fnote.md&token=csrf-token");
  });

  it("posts to the remove route when the note is already pinned", () => {
    renderNotePinFixture("pinned");
    const container = document.querySelector<HTMLElement>("[data-pin-note]");
    const request = buildNotePinToggleRequest(readNotePinConfig(container!));
    expect(request.url).toBe("/api/pins/remove");
  });
});

describe("note pin DOM behavior", () => {
  it("flips the button state and reports the server message on success", async () => {
    renderNotePinFixture("unpinned");
    const fetchMock = vi.fn(async () => pinResponse({ ok: true, message: "Pinned note." }));

    initNotePinAction({ fetch: fetchMock as typeof fetch });
    const button = document.querySelector<HTMLButtonElement>("[data-pin-button]");
    const status = document.querySelector<HTMLElement>("[data-pin-status]");
    button?.click();

    expect(button?.disabled).toBe(true);
    expect(status?.textContent).toBe("Pinning note…");

    await waitForExpect(() => {
      expect(button?.disabled).toBe(false);
    });

    const [url] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/pins/set");
    expect(button?.getAttribute("aria-pressed")).toBe("true");
    expect(button?.textContent).toBe("Unpin");
    expect(
      document.querySelector<HTMLElement>("[data-pin-note]")?.getAttribute("data-pin-state")
    ).toBe("pinned");
    expect(status?.textContent).toBe("Pinned note.");
    expect(status?.classList.contains("is-success")).toBe(true);
  });

  it("unpins a pinned note and posts to the remove route", async () => {
    renderNotePinFixture("pinned");
    const fetchMock = vi.fn(async () => pinResponse({ ok: true, message: "Unpinned note." }));

    initNotePinAction({ fetch: fetchMock as typeof fetch });
    const button = document.querySelector<HTMLButtonElement>("[data-pin-button]");
    button?.click();

    await waitForExpect(() => {
      expect(button?.getAttribute("aria-pressed")).toBe("false");
    });

    const [url] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/pins/remove");
    expect(button?.textContent).toBe("Pin");
    expect(
      document.querySelector<HTMLElement>("[data-pin-note]")?.getAttribute("data-pin-state")
    ).toBe("unpinned");
  });

  it("keeps the previous state and shows the error message on a failure", async () => {
    renderNotePinFixture("unpinned");
    const fetchMock = vi.fn(async () =>
      pinResponse({ ok: false, code: "conflict", message: "the pinned list changed" }, 409)
    );

    initNotePinAction({ fetch: fetchMock as typeof fetch });
    const button = document.querySelector<HTMLButtonElement>("[data-pin-button]");
    const status = document.querySelector<HTMLElement>("[data-pin-status]");
    button?.click();

    await waitForExpect(() => {
      expect(button?.disabled).toBe(false);
    });

    expect(button?.getAttribute("aria-pressed")).toBe("false");
    expect(status?.textContent).toBe("the pinned list changed");
    expect(status?.classList.contains("is-error")).toBe(true);
  });

  it("does nothing when the pin group is absent", () => {
    document.body.innerHTML = "";
    const fetchMock = vi.fn();
    expect(() => initNotePinAction({ fetch: fetchMock as unknown as typeof fetch })).not.toThrow();
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
