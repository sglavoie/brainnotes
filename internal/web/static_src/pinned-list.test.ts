// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import { initPinnedList, pinnedNeighborKeys } from "./pinned-list";
import { waitForExpect } from "./shared.test-helpers";

const pinResponse = (body: unknown, status = 200): Response =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

const renderPinnedFixture = (): void => {
  const rows = ["a.md", "b.md", "c.md"]
    .map(
      (key) => `
        <li class="pinned-list-item" data-pinned-item data-pinned-key="${key}">
          <button type="button" data-pinned-handle>grip</button>
          <a href="/n/${key}">${key}</a>
          <button type="button" class="pin-button" data-pin-button aria-label="Unpin ${key}">Unpin</button>
        </li>`
    )
    .join("");
  document.body.innerHTML = `
    <section data-pinned-root data-pinned-csrf="csrf-token">
      <p data-pinned-hint>Reorder pins</p>
      <p data-pinned-empty hidden>No pins. <a href="/library">Library</a></p>
      <ol data-pinned-list>${rows}</ol>
      <p data-pinned-status> </p>
    </section>
  `;
};

const renderedKeys = (): string[] =>
  Array.from(document.querySelectorAll("[data-pinned-item]")).map(
    (item) => item.getAttribute("data-pinned-key") || ""
  );

const rowFor = (key: string): HTMLElement =>
  document.querySelector<HTMLElement>(`[data-pinned-key="${key}"]`)!;

const handleFor = (key: string): HTMLButtonElement =>
  rowFor(key).querySelector<HTMLButtonElement>("[data-pinned-handle]")!;

const stubRowRects = (): void => {
  Array.from(document.querySelectorAll<HTMLElement>("[data-pinned-item]")).forEach(
    (item, index) => {
      const top = index * 40;
      item.getBoundingClientRect = (): DOMRect =>
        ({
          top,
          bottom: top + 40,
          height: 40,
          left: 0,
          right: 200,
          width: 200,
          x: 0,
          y: top,
          toJSON: () => ({}),
        }) as DOMRect;
    }
  );
};

const pointerEvent = (type: string, clientY: number): MouseEvent =>
  new MouseEvent(type, { bubbles: true, cancelable: true, button: 0, clientY });

const deferredFetch = (): {
  mock: ReturnType<typeof vi.fn>;
  settle: (index: number, body: unknown, status?: number) => Promise<void>;
} => {
  const resolvers: Array<(response: Response) => void> = [];
  const mock = vi.fn(
    () =>
      new Promise<Response>((resolve) => {
        resolvers.push(resolve);
      })
  );
  const settle = async (index: number, body: unknown, status = 200): Promise<void> => {
    await waitForExpect(() => {
      expect(resolvers.length).toBeGreaterThan(index);
    });
    resolvers[index]!(pinResponse(body, status));
    await new Promise((resolve) => setTimeout(resolve, 0));
  };
  return { mock, settle };
};

const flushMicrotasks = (): Promise<void> =>
  new Promise((resolve) => {
    setTimeout(resolve, 0);
  });

const arrowDown = (key: string): void => {
  handleFor(key).dispatchEvent(
    new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true, cancelable: true })
  );
};

const requestBody = (call: unknown): string => {
  const [, init] = call as [string, RequestInit];
  return String(init.body);
};

afterEach(() => {
  document.body.innerHTML = "";
});

describe("pinned list neighbors", () => {
  it("reads the adjacent keys and reports an end of the list as empty", () => {
    renderPinnedFixture();
    expect(pinnedNeighborKeys(rowFor("b.md"))).toEqual({ before: "a.md", after: "c.md" });
    expect(pinnedNeighborKeys(rowFor("a.md"))).toEqual({ before: "", after: "b.md" });
    expect(pinnedNeighborKeys(rowFor("c.md"))).toEqual({ before: "b.md", after: "" });
  });
});

describe("pinned list keyboard reorder", () => {
  it("moves a row down and posts the new neighbors", async () => {
    renderPinnedFixture();
    const fetchMock = vi.fn(async () => pinResponse({ ok: true, message: "Reordered pins." }));

    initPinnedList({ fetch: fetchMock as typeof fetch });
    handleFor("a.md").dispatchEvent(
      new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true, cancelable: true })
    );

    expect(renderedKeys()).toEqual(["b.md", "a.md", "c.md"]);
    await waitForExpect(() => {
      expect(document.querySelector("[data-pinned-status]")?.textContent).toBe("Moved a.md to position 2 of 3.");
    });

    const [url] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/pins/reorder");
    expect(requestBody(fetchMock.mock.calls[0])).toBe(
      "key=a.md&token=csrf-token&before=b.md&after=c.md"
    );
  });

  it("moves a row up and posts an empty leading neighbor at the top", async () => {
    renderPinnedFixture();
    const fetchMock = vi.fn(async () => pinResponse({ ok: true, message: "Reordered pins." }));

    initPinnedList({ fetch: fetchMock as typeof fetch });
    handleFor("b.md").dispatchEvent(
      new KeyboardEvent("keydown", { key: "ArrowUp", bubbles: true, cancelable: true })
    );

    expect(renderedKeys()).toEqual(["b.md", "a.md", "c.md"]);
    await waitForExpect(() => {
      expect(fetchMock).toHaveBeenCalledTimes(1);
    });
    expect(requestBody(fetchMock.mock.calls[0])).toBe(
      "key=b.md&token=csrf-token&before=&after=a.md"
    );
  });

  it("keeps focus on the moved handle so the next arrow key moves it again", async () => {
    renderPinnedFixture();
    const fetchMock = vi.fn(async () => pinResponse({ ok: true, message: "Reordered pins." }));

    initPinnedList({ fetch: fetchMock as typeof fetch });
    const handle = handleFor("c.md");
    handle.focus();
    handle.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowUp", bubbles: true, cancelable: true }));
    expect(document.activeElement).toBe(handle);
    handle.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowUp", bubbles: true, cancelable: true }));

    expect(renderedKeys()).toEqual(["c.md", "a.md", "b.md"]);
    expect(document.activeElement).toBe(handle);
    await waitForExpect(() => {
      expect(fetchMock).toHaveBeenCalledTimes(2);
    });
  });

  it("does not post when the row is already at the end of the list", () => {
    renderPinnedFixture();
    const fetchMock = vi.fn(async () => pinResponse({ ok: true }));

    initPinnedList({ fetch: fetchMock as typeof fetch });
    handleFor("c.md").dispatchEvent(
      new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true, cancelable: true })
    );

    expect(renderedKeys()).toEqual(["a.md", "b.md", "c.md"]);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("restores the previous order and reports the error when the server refuses", async () => {
    renderPinnedFixture();
    const fetchMock = vi.fn(async () =>
      pinResponse({ ok: false, code: "conflict", message: "the pinned list changed" }, 409)
    );

    initPinnedList({ fetch: fetchMock as typeof fetch });
    handleFor("a.md").dispatchEvent(
      new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true, cancelable: true })
    );
    expect(renderedKeys()).toEqual(["b.md", "a.md", "c.md"]);

    await waitForExpect(() => {
      expect(renderedKeys()).toEqual(["a.md", "b.md", "c.md"]);
    });
    const status = document.querySelector<HTMLElement>("[data-pinned-status]");
    expect(status?.textContent).toBe("the pinned list changed");
    expect(status?.classList.contains("is-error")).toBe(true);
  });
});

describe("pinned list reorder sequencing", () => {
  it("holds a rapid second move until the first request settles so the server sees action order", async () => {
    renderPinnedFixture();
    const { mock, settle } = deferredFetch();

    initPinnedList({ fetch: mock as unknown as typeof fetch });
    arrowDown("a.md");
    arrowDown("a.md");

    expect(renderedKeys()).toEqual(["b.md", "c.md", "a.md"]);
    await flushMicrotasks();
    expect(mock).toHaveBeenCalledTimes(1);
    expect(requestBody(mock.mock.calls[0])).toBe("key=a.md&token=csrf-token&before=b.md&after=c.md");

    await settle(0, { ok: true, message: "Reordered pins." });
    expect(mock).toHaveBeenCalledTimes(2);
    expect(requestBody(mock.mock.calls[1])).toBe("key=a.md&token=csrf-token&before=c.md&after=");

    await settle(1, { ok: true, message: "Reordered pins." });
    expect(renderedKeys()).toEqual(["b.md", "c.md", "a.md"]);
    await waitForExpect(() => {
      expect(document.querySelector("[data-pinned-status]")?.textContent).toBe("Moved a.md to position 3 of 3.");
    });
  });

  it("rolls back only the last move when an earlier rapid move succeeded", async () => {
    renderPinnedFixture();
    const { mock, settle } = deferredFetch();
    const reloadMock = vi.fn();

    initPinnedList({ fetch: mock as unknown as typeof fetch, reload: reloadMock });
    arrowDown("a.md");
    await settle(0, { ok: true, message: "Reordered pins." });
    arrowDown("a.md");
    expect(renderedKeys()).toEqual(["b.md", "c.md", "a.md"]);

    await settle(1, { ok: false, code: "conflict", message: "the pinned list changed" }, 409);

    expect(renderedKeys()).toEqual(["b.md", "a.md", "c.md"]);
    const status = document.querySelector<HTMLElement>("[data-pinned-status]");
    expect(status?.textContent).toBe("the pinned list changed");
    expect(status?.classList.contains("is-error")).toBe(true);
    expect(reloadMock).not.toHaveBeenCalled();
  });

  it("reloads instead of rolling back when a queued newer move already changed the list", async () => {
    renderPinnedFixture();
    const { mock, settle } = deferredFetch();
    const reloadMock = vi.fn();

    initPinnedList({ fetch: mock as unknown as typeof fetch, reload: reloadMock });
    arrowDown("a.md");
    arrowDown("a.md");

    await settle(0, { ok: false, code: "conflict", message: "the pinned list changed" }, 409);

    expect(renderedKeys()).toEqual(["b.md", "c.md", "a.md"]);
    expect(mock).toHaveBeenCalledTimes(1);
    expect(reloadMock).toHaveBeenCalledTimes(1);
    expect(document.querySelector("[data-pinned-status]")?.textContent).toBe(
      "the pinned list changed"
    );
  });
});

describe("pinned list drag reorder", () => {
  it("moves the dragged row past the rows the pointer crossed and posts on release", async () => {
    renderPinnedFixture();
    stubRowRects();
    const fetchMock = vi.fn(async () => pinResponse({ ok: true, message: "Reordered pins." }));

    initPinnedList({ fetch: fetchMock as typeof fetch });
    handleFor("a.md").dispatchEvent(pointerEvent("pointerdown", 20));
    expect(rowFor("a.md").classList.contains("is-dragging")).toBe(true);

    window.dispatchEvent(pointerEvent("pointermove", 70));
    expect(renderedKeys()).toEqual(["b.md", "a.md", "c.md"]);
    expect(rowFor("c.md").classList.contains("is-drop-target")).toBe(true);

    window.dispatchEvent(pointerEvent("pointerup", 70));
    expect(rowFor("a.md").classList.contains("is-dragging")).toBe(false);
    expect(rowFor("c.md").classList.contains("is-drop-target")).toBe(false);

    await waitForExpect(() => {
      expect(fetchMock).toHaveBeenCalledTimes(1);
    });
    expect(requestBody(fetchMock.mock.calls[0])).toBe(
      "key=a.md&token=csrf-token&before=b.md&after=c.md"
    );
  });

  it("posts nothing when the drag ends where it started", () => {
    renderPinnedFixture();
    stubRowRects();
    const fetchMock = vi.fn(async () => pinResponse({ ok: true }));

    initPinnedList({ fetch: fetchMock as typeof fetch });
    handleFor("b.md").dispatchEvent(pointerEvent("pointerdown", 60));
    window.dispatchEvent(pointerEvent("pointermove", 60));
    window.dispatchEvent(pointerEvent("pointerup", 60));

    expect(renderedKeys()).toEqual(["a.md", "b.md", "c.md"]);
    expect(fetchMock).not.toHaveBeenCalled();
  });
});

describe("pinned list unpin", () => {
  it("removes a row without reloading and focuses the next unpin button", async () => {
    renderPinnedFixture();
    const fetchMock = vi.fn(async () => pinResponse({ ok: true, message: "Unpinned note." }));
    const reloadMock = vi.fn();

    initPinnedList({ fetch: fetchMock as typeof fetch, reload: reloadMock });
    const button = rowFor("b.md").querySelector<HTMLButtonElement>("[data-pin-button]")!;
    button.focus();
    button.click();

    await waitForExpect(() => {
      expect(renderedKeys()).toEqual(["a.md", "c.md"]);
    });
    expect(reloadMock).not.toHaveBeenCalled();
    expect(document.activeElement).toBe(rowFor("c.md").querySelector("[data-pin-button]"));
    const [url] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/pins/remove");
    expect(requestBody(fetchMock.mock.calls[0])).toBe("key=b.md&token=csrf-token");
  });

  it("waits for pending moves before unpinning and blocks further moves", async () => {
    renderPinnedFixture();
    const { mock, settle } = deferredFetch();
    initPinnedList({ fetch: mock as unknown as typeof fetch });
    arrowDown("a.md");
    rowFor("b.md").querySelector<HTMLButtonElement>("[data-pin-button]")!.click();
    arrowDown("a.md");
    await flushMicrotasks();
    expect(mock).toHaveBeenCalledTimes(1);
    expect(renderedKeys()).toEqual(["b.md", "a.md", "c.md"]);
    await settle(0, { ok: true });
    expect((mock.mock.calls[1] as unknown as [string])[0]).toBe("/api/pins/remove");
    await settle(1, { ok: true });
    expect(renderedKeys()).toEqual(["a.md", "c.md"]);
  });

  it("keeps the page when the unpin fails", async () => {
    renderPinnedFixture();
    const fetchMock = vi.fn(async () =>
      pinResponse({ ok: false, code: "not_found", message: "note is missing" }, 404)
    );
    const reloadMock = vi.fn();

    initPinnedList({ fetch: fetchMock as typeof fetch, reload: reloadMock });
    rowFor("c.md").querySelector<HTMLButtonElement>("[data-pin-button]")?.click();

    await waitForExpect(() => {
      expect(document.querySelector("[data-pinned-status]")?.textContent).toBe("note is missing");
    });
    expect(reloadMock).not.toHaveBeenCalled();
  });

  it("does nothing when the pinned root is absent", () => {
    document.body.innerHTML = "";
    const fetchMock = vi.fn();
    expect(() => initPinnedList({ fetch: fetchMock as unknown as typeof fetch })).not.toThrow();
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
