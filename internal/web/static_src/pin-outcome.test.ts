// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { initNotePinAction } from "./note-pin";
import { initPinnedList } from "./pinned-list";
import { renderOverviewPin } from "./overview-search-pin";
import type { SearchResult } from "./api";

const setup = (surface: string, fetcher: typeof fetch) => {
  vi.stubGlobal("fetch", fetcher);
  if (surface === "library") {
    document.body.innerHTML = '<div data-overview-csrf="token"></div>';
    const row = renderOverviewPin(document, { key: "a", title: "Alpha", pinned: true } as SearchResult);
    document.body.append(row.button, row.feedback);
  } else if (surface === "note") {
    document.body.innerHTML = `<div data-pin-note data-pin-key="a" data-pin-csrf="token" data-pin-state="pinned">
      <button data-pin-button aria-pressed="true">Unpin</button><p data-pin-status></p></div>`;
    initNotePinAction({ fetch: fetcher });
  } else {
    document.body.innerHTML = `<section data-pinned-root data-pinned-csrf="token">
      <ol data-pinned-list><li data-pinned-item data-pinned-key="a"><button data-pin-button>Unpin</button></li></ol>
      <p data-pinned-status></p></section>`;
    initPinnedList({ fetch: fetcher });
  }
  return document.querySelector<HTMLButtonElement>("[data-pin-button]")!;
};

const json = (body: unknown) => new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
const cases = [
  { name: "invalid JSON", response: () => new Response("{", { headers: { "Content-Type": "application/json" } }), unknown: true },
  { name: "HTML", response: () => new Response("<html>proxy</html>"), unknown: true },
  { name: "missing confirmation", response: () => json({}), unknown: true },
  { name: "nonboolean confirmation", response: () => json({ ok: "false" }), unknown: true },
  { name: "refusal", response: () => json({ ok: false, message: "Refused." }), unknown: false },
  { name: "lost response", response: () => { throw new Error("offline"); }, unknown: true },
];

afterEach(() => { document.body.innerHTML = ""; vi.unstubAllGlobals(); });

for (const surface of ["note", "library", "pinned"]) {
  it.each(cases)(`${surface} preserves the note after $name`, async (test) => {
    const fetcher = vi.fn(async () => test.response());
    const button = setup(surface, fetcher);
    button.click();
    await vi.waitFor(() => expect(document.body.textContent).toContain(test.unknown ? "Couldn't confirm" : "Refused."));
    expect(button.isConnected).toBe(true);
    expect(button.textContent).toBe("Unpin");
    expect(document.body.textContent).not.toContain("Unpinned note.");
    expect(document.querySelector("a")?.textContent).toBe(test.unknown ? "Refresh to check" : undefined);
    expect(fetcher).toHaveBeenCalledTimes(1);
    if (surface === "library" && test.unknown) {
      expect(document.querySelector<HTMLButtonElement>(".overview-pin-feedback button")!.hidden).toBe(true);
    }
  });
}

it("stops queued reorders and unpin after an unconfirmed move", async () => {
  document.body.innerHTML = `<section data-pinned-root data-pinned-csrf="token"><ol data-pinned-list>
    ${["a", "b", "c"].map(key => `<li data-pinned-item data-pinned-key="${key}"><button data-pinned-handle>Move</button><button data-pin-button>Unpin</button></li>`).join("")}
    </ol><p data-pinned-status></p></section>`;
  const fetcher = vi.fn(async () => json({}));
  initPinnedList({ fetch: fetcher });
  const handle = document.querySelector("[data-pinned-handle]")!;
  for (let n = 0; n < 2; n++) handle.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true }));
  document.querySelector<HTMLButtonElement>("[data-pin-button]")!.click();
  await vi.waitFor(() => expect(document.body.textContent).toContain("Refresh to check"));
  await new Promise(resolve => setTimeout(resolve, 0));
  expect(fetcher).toHaveBeenCalledTimes(1);
  expect(document.querySelectorAll("[data-pinned-item]")).toHaveLength(3);
});
