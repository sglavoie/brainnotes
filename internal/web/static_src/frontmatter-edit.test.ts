// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import {
  buildFrontmatterSetFetchRequest,
  buildFrontmatterUnsetFetchRequest,
  initFrontmatterEditing,
  readFrontmatterSetConfig,
  readFrontmatterUnsetConfig,
} from "./frontmatter-edit";
import { waitForExpect } from "./shared.test-helpers";

const frontmatterResponse = (body: unknown, status = 200): Response =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

afterEach(() => {
  document.body.innerHTML = "";
});

describe("frontmatter request construction", () => {
  it("reads set config and builds the set request form", () => {
    document.body.innerHTML = `
      <section data-frontmatter-panel data-frontmatter-key="source" data-frontmatter-csrf="csrf-token">
        <form data-frontmatter-set data-frontmatter-property="tags" data-frontmatter-op="+=">
          <input name="value" value="beta" />
        </form>
      </section>
    `;
    const form = document.querySelector<HTMLFormElement>("[data-frontmatter-set]");
    expect(form).not.toBeNull();

    const config = readFrontmatterSetConfig(form!);
    const request = buildFrontmatterSetFetchRequest(config);
    const headers = request.init.headers as Record<string, string>;

    expect(config).toEqual({
      noteKey: "source",
      csrfToken: "csrf-token",
      property: "tags",
      op: "+=",
      value: "beta",
    });
    expect(request.url).toBe("/api/notes/frontmatter/set");
    expect(request.init.method).toBe("POST");
    expect(headers.Accept).toBe("application/json");
    expect(headers["Content-Type"]).toBe(
      "application/x-www-form-urlencoded; charset=UTF-8"
    );
    expect(headers["X-BN-CSRF"]).toBe("csrf-token");
    expect(request.init.body).toBe(
      "key=source&token=csrf-token&property=tags&op=%2B%3D&value=beta"
    );
  });

  it("reads unset config and builds the unset request form", () => {
    document.body.innerHTML = `
      <section data-frontmatter-panel data-frontmatter-key="source" data-frontmatter-csrf="csrf-token">
        <form data-frontmatter-unset data-frontmatter-property="status"></form>
      </section>
    `;
    const form = document.querySelector<HTMLFormElement>("[data-frontmatter-unset]");
    expect(form).not.toBeNull();

    const config = readFrontmatterUnsetConfig(form!);
    const request = buildFrontmatterUnsetFetchRequest(config);

    expect(config).toEqual({
      noteKey: "source",
      csrfToken: "csrf-token",
      property: "status",
    });
    expect(request.url).toBe("/api/notes/frontmatter/unset");
    expect(request.init.body).toBe("key=source&token=csrf-token&property=status");
  });
});

describe("frontmatter DOM behavior", () => {
  it("posts the configured set request and shows success", async () => {
    document.body.innerHTML = `
      <section data-frontmatter-panel data-frontmatter-key="source" data-frontmatter-csrf="csrf-token">
        <p data-frontmatter-status> </p>
        <form data-frontmatter-set data-frontmatter-property="status" data-frontmatter-op="=">
          <input name="value" value="done" />
          <button type="submit">Set</button>
        </form>
      </section>
    `;
    const fetchMock = vi.fn(async () =>
      frontmatterResponse({ ok: true, message: "Updated frontmatter." })
    );

    initFrontmatterEditing({ fetch: fetchMock as typeof fetch });
    const form = document.querySelector<HTMLFormElement>("[data-frontmatter-set]");
    const button = document.querySelector<HTMLButtonElement>("button");
    const status = document.querySelector<HTMLElement>("[data-frontmatter-status]");
    form?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));

    expect(button?.disabled).toBe(true);
    expect(status?.textContent).toBe("Updating frontmatter…");

    await waitForExpect(() => {
      expect(button?.disabled).toBe(false);
    });

    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/notes/frontmatter/set");
    expect(init).toMatchObject({
      method: "POST",
      body: "key=source&token=csrf-token&property=status&op=%3D&value=done",
    });
    expect(status?.textContent).toBe("Updated frontmatter. Refresh properties");
    expect(status?.classList.contains("is-success")).toBe(true);
  });

  it("shows server error messages for unset", async () => {
    document.body.innerHTML = `
      <section data-frontmatter-panel data-frontmatter-key="source" data-frontmatter-csrf="csrf-token">
        <p data-frontmatter-status> </p>
        <form data-frontmatter-unset data-frontmatter-property="status">
          <button type="submit">Unset</button>
        </form>
      </section>
    `;
    const fetchMock = vi.fn(async () =>
      frontmatterResponse({ ok: false, code: "conflict", message: "note is missing" }, 409)
    );

    initFrontmatterEditing({ fetch: fetchMock as typeof fetch });
    const form = document.querySelector<HTMLFormElement>("[data-frontmatter-unset]");
    const button = document.querySelector<HTMLButtonElement>("button");
    const status = document.querySelector<HTMLElement>("[data-frontmatter-status]");
    form?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));

    await waitForExpect(() => {
      expect(button?.disabled).toBe(false);
    });

    expect(status?.textContent).toBe("note is missing");
    expect(status?.classList.contains("is-error")).toBe(true);
  });
});
