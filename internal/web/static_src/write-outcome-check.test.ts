// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from "vitest";
import { initPendingWriteOutcomes, newWriteOperationID, offerWriteOutcomeCheck } from "./write-outcome-check";

beforeEach(() => sessionStorage.clear());

it("uses one opaque ID and confirms only the matching journaled attempt", async () => {
  const id = newWriteOperationID();
  expect(id).toMatch(/^[0-9a-f]{32}$/);
  document.body.innerHTML = '<p role="status" data-write-status>Creation not confirmed.</p>';
  const status = document.querySelector<HTMLElement>("[data-write-status]")!;
  const fetchedURLs: string[] = [];
  const fetcher = vi.fn(async (url: string) => {
    fetchedURLs.push(url);
    return new Response(JSON.stringify({
      ok: true, status: "confirmed", journal_id: "entry-123",
    }), { headers: { "Content-Type": "application/json" } });
  });

  offerWriteOutcomeCheck(status, id, fetcher, "Creation");
  status.querySelector<HTMLButtonElement>("button")!.click();
  await vi.waitFor(() => expect(status.textContent).toContain("Creation confirmed in History"));
  expect(fetchedURLs[0]).toBe(`/api/operations/outcome?id=${id}`);
  expect(status.querySelector("button")).toBeNull();
});

it("keeps Check outcome available when the journal has no completed record yet", async () => {
  document.body.innerHTML = '<p role="status" data-write-status>Change not confirmed.</p>';
  const status = document.querySelector<HTMLElement>("[data-write-status]")!;
  const fetcher = vi.fn(async () => new Response(JSON.stringify({ ok: true, status: "unconfirmed" }), {
    headers: { "Content-Type": "application/json" },
  }));
  offerWriteOutcomeCheck(status, "0123456789abcdef0123456789abcdef", fetcher);
  status.querySelector<HTMLButtonElement>("button")!.click();
  await vi.waitFor(() => expect(status.textContent).toContain("still unconfirmed"));
  expect(status.querySelector<HTMLButtonElement>("button")?.disabled).toBe(false);
});

it("restores a check after reload in the same tab without storing note text", () => {
  const id = "0123456789abcdef0123456789abcdef";
  document.body.innerHTML = '<main><header>Header</header><p data-write-status>Change not confirmed.</p></main>';
  const status = document.querySelector<HTMLElement>("[data-write-status]")!;
  offerWriteOutcomeCheck(status, id, vi.fn(), "Trash");
  expect(sessionStorage.getItem("bn.pending-write-outcomes")).toContain(id);
  document.body.innerHTML = '<main><header>Header</header></main>';
  initPendingWriteOutcomes(document, vi.fn());
  expect(document.querySelector(".pending-write-outcomes")?.textContent).toContain("Trash was not confirmed");
  expect(document.querySelector(".pending-write-outcomes button")?.textContent).toBe("Check outcome");
});
