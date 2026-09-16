// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { normalizeBriefItemResponse } from "./api";
import { runOpen, type BriefActionContext } from "./daily-brief-actions";

afterEach(() => vi.unstubAllGlobals());

it("retains browser destinations without changing local-editor responses", () => {
  expect(normalizeBriefItemResponse({ok: true, action: "open", destination: "/n/Note"}).destination).toBe("/n/Note");
  expect(normalizeBriefItemResponse({ok: true, action: "open"}).destination).toBe("");
});

it("navigates to the rendered note after recording a replica open", async () => {
  const assign = vi.fn();
  vi.stubGlobal("window", {location: {assign}});
  const button = document.createElement("button");
  button.setAttribute("data-brief-slot", "continue");
  button.setAttribute("data-brief-card-id", "card-1");
  const ctx = {
    config: {csrfToken: "test", itemEndpoint: "/api/brief/item"},
    fetcher: vi.fn(async () => new Response(JSON.stringify({ok: true, action: "open", destination: "/n/Note"}), {headers: {"Content-Type": "application/json"}})),
    setGlobalStatus: vi.fn(), setSlotStatus: vi.fn(), setSlotsBusy: vi.fn(),
    updateActivity: vi.fn(), markCardOpened: vi.fn(), openedCards: new Set<string>(),
  } as unknown as BriefActionContext;
  await runOpen(ctx, button);
  expect(ctx.updateActivity).toHaveBeenCalledOnce();
  expect(ctx.openedCards.has("card-1")).toBe(true);
  expect(assign).toHaveBeenCalledWith("/n/Note");
});
