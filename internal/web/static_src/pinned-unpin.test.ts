// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { initPinnedList } from "./pinned-list";

const setup = () => {
  document.body.innerHTML = `
    <section data-pinned-root data-pinned-csrf="token">
      <p data-pinned-hint>Reorder</p>
      <ol data-pinned-list>
        <li data-pinned-item data-pinned-key="a">
          <a href="/n/a">Alpha</a><button data-pinned-handle>Move</button>
          <button data-pin-button>Unpin</button>
        </li>
      </ol>
      <p data-pinned-empty hidden>No pins. <a href="/library">Library</a></p>
      <p data-pinned-status></p>
    </section><input id="elsewhere">
  `;
  let resolve!: (response: Response) => void;
  const fetcher = vi.fn(() => new Promise<Response>((done) => { resolve = done; }));
  const reload = vi.fn();
  initPinnedList({ fetch: fetcher, reload });
  const button = document.querySelector<HTMLButtonElement>("[data-pin-button]")!;
  button.focus();
  button.click();
  return { button, fetcher, reload, settle: async (status = 200) => {
    await vi.waitFor(() => expect(fetcher).toHaveBeenCalledTimes(1));
    resolve(new Response(JSON.stringify({ ok: status === 200 }), { status }));
    await vi.waitFor(() => expect(button.hasAttribute("aria-disabled")).toBe(false));
  } };
};

afterEach(() => { document.body.innerHTML = ""; });

it("shows the empty state after the last unpin and focuses Library", async () => {
  const test = setup();
  test.button.click();
  await test.settle();
  expect(test.fetcher).toHaveBeenCalledTimes(1);
  expect(test.reload).not.toHaveBeenCalled();
  expect(document.querySelector<HTMLElement>("[data-pinned-list]")!.hidden).toBe(true);
  expect(document.querySelector<HTMLElement>("[data-pinned-hint]")!.hidden).toBe(true);
  expect(document.querySelector<HTMLElement>("[data-pinned-empty]")!.hidden).toBe(false);
  expect(document.activeElement).toBe(document.querySelector("[data-pinned-empty] a"));
});

it("preserves focus moved elsewhere during the request", async () => {
  const test = setup();
  const elsewhere = document.querySelector<HTMLInputElement>("#elsewhere")!;
  elsewhere.focus();
  await test.settle();
  expect(document.activeElement).toBe(elsewhere);
});

it("keeps the row and focus when unpin fails", async () => {
  const test = setup();
  await test.settle(409);
  expect(test.button.isConnected).toBe(true);
  expect(document.activeElement).toBe(test.button);
});
