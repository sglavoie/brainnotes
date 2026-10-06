// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { initRelatedNotes } from "./related";
import { deferred, jsonResponse, payload, requestOptions, rootHTML, waitForExpect } from "./related.test-helpers";

const retryButton = (): HTMLButtonElement => document.querySelector<HTMLButtonElement>("[data-related-retry]")!;
const status = (): HTMLElement => document.querySelector<HTMLElement>("[data-related-status]")!;
const tab = (mode: string): HTMLButtonElement => document.querySelector<HTMLButtonElement>(`[data-related-tab="${mode}"]`)!;

afterEach(() => { document.body.innerHTML = ""; });

it("retries a failed initial load once and moves focus to the result status on success", async () => {
  document.body.innerHTML = rootHTML();
  const pending = deferred<Response>();
  const fetcher = vi.fn().mockRejectedValueOnce(new Error("Offline")).mockReturnValueOnce(pending.promise);
  initRelatedNotes({ fetch: fetcher });
  await waitForExpect(() => expect(retryButton().hidden).toBe(false));
  expect(status().textContent).toBe("Offline");
  retryButton().focus();
  retryButton().click();
  retryButton().click();
  expect(retryButton().disabled).toBe(true);
  expect(fetcher).toHaveBeenCalledTimes(2);
  expect(fetcher.mock.calls[1][0]).toBe(fetcher.mock.calls[0][0]);
  pending.resolve(jsonResponse(payload("balanced", 5)));
  await waitForExpect(() => expect(retryButton().hidden).toBe(true));
  expect(document.querySelectorAll("[data-related-card]")).toHaveLength(5);
  expect(document.activeElement).toBe(status());
});

it("preserves cards and retries the failed expansion size across repeated failures", async () => {
  document.body.innerHTML = rootHTML();
  const pending = deferred<Response>();
  const fetcher = vi.fn()
    .mockResolvedValueOnce(jsonResponse(payload("balanced", 5)))
    .mockResolvedValueOnce(jsonResponse({ message: "Unavailable" }, 503))
    .mockRejectedValueOnce(new Error("Still offline"))
    .mockReturnValueOnce(pending.promise);
  initRelatedNotes({ fetch: fetcher });
  await waitForExpect(() => expect(document.querySelectorAll("[data-related-card]")).toHaveLength(5));
  document.querySelector<HTMLButtonElement>('[data-related-show-more="balanced"]')!.click();
  await waitForExpect(() => expect(retryButton().hidden).toBe(false));
  retryButton().click();
  await waitForExpect(() => expect(status().textContent).toBe("Still offline"));
  expect(retryButton().hidden).toBe(false);
  expect(retryButton().disabled).toBe(false);
  retryButton().click();
  expect(document.querySelectorAll("[data-related-card]")).toHaveLength(5);
  expect(fetcher.mock.calls.slice(1).map(([input]) => requestOptions(input).limit)).toEqual([10, 10, 10]);
  tab("balanced").focus();
  pending.resolve(jsonResponse(payload("balanced", 10)));
  await waitForExpect(() => expect(retryButton().hidden).toBe(true));
  expect(document.querySelectorAll("[data-related-card]")).toHaveLength(10);
  expect(document.activeElement).toBe(tab("balanced"));
});

it("keeps a failed background expansion separate from the active mode", async () => {
  document.body.innerHTML = rootHTML();
  const expansion = deferred<Response>();
  const fetcher = vi.fn(async (input: RequestInfo | URL) => {
    const { mode, limit } = requestOptions(input);
    if (mode === "balanced" && limit === 10) return expansion.promise;
    return jsonResponse(payload(mode, limit));
  });
  initRelatedNotes({ fetch: fetcher });
  await waitForExpect(() => expect(document.querySelectorAll("[data-related-card]")).toHaveLength(5));
  document.querySelector<HTMLButtonElement>('[data-related-show-more="balanced"]')!.click();
  tab("connect").click();
  await waitForExpect(() => expect(document.querySelectorAll('[data-related-results="connect"] [data-related-card]')).toHaveLength(5));
  expansion.resolve(jsonResponse({ message: "Expansion failed" }, 503));
  await new Promise((resolve) => setTimeout(resolve, 0));
  expect(retryButton().hidden).toBe(true);
  expect(status().textContent).toBe("Showing 5 related notes.");
  tab("balanced").click();
  await waitForExpect(() => expect(retryButton().hidden).toBe(false));
  retryButton().click();
  expect(requestOptions(fetcher.mock.calls.at(-1)![0])).toEqual({ mode: "balanced", limit: 10 });
  await waitForExpect(() => expect(retryButton().disabled).toBe(false));
});
