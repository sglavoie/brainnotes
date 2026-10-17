// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { bindSyncRefresh } from "./sync-line-refresh";
import { syncStatus } from "./sync.test-helpers";
import { announceConfirmedWrite } from "./write-notice";
import type { SyncStatusResult } from "./api-types";
import type { SyncOutcome } from "./sync-requests";

let dispose = (): void => {};
const visibility = (value: string): void => {
  Object.defineProperty(document, "visibilityState", { value, configurable: true });
  document.dispatchEvent(new Event("visibilitychange"));
};
beforeEach(() => {
  vi.useFakeTimers();
  document.body.innerHTML = '<p id="line"></p>';
  visibility("visible");
});
afterEach(() => { dispose(); vi.useRealTimers(); });

const setup = (read: () => Promise<SyncOutcome<SyncStatusResult>>): ReturnType<typeof vi.fn> => {
  const render = vi.fn();
  dispose = bindSyncRefresh({ doc: document, line: document.getElementById("line")!, read, render });
  return render;
};
const pendingStatus = (): SyncStatusResult => {
  const result = syncStatus();
  result.transport.awaiting_receipt = 1;
  return result;
};

it("refreshes after a confirmed write and observes the companion's delayed work", async () => {
  const read = vi.fn(async () => ({ ok: true as const, result: syncStatus() }));
  setup(read);
  await vi.advanceTimersByTimeAsync(10_000);
  expect(read).toHaveBeenCalledTimes(1);
  announceConfirmedWrite();
  await vi.advanceTimersByTimeAsync(10_000);
  expect(read).toHaveBeenCalledTimes(7);
  await vi.advanceTimersByTimeAsync(10_000);
  expect(read).toHaveBeenCalledTimes(7);
});

it("follows pending work, stops when it settles, and bounds a permanently pending status", async () => {
  const read = vi.fn(async () => ({ ok: true as const, result: pendingStatus() }));
  setup(read);
  await vi.advanceTimersByTimeAsync(2000);
  expect(read).toHaveBeenCalledTimes(2);
  read.mockResolvedValue({ ok: true, result: syncStatus() });
  await vi.advanceTimersByTimeAsync(10_000);
  expect(read).toHaveBeenCalledTimes(3);
  read.mockResolvedValue({ ok: true, result: pendingStatus() });
  announceConfirmedWrite();
  await vi.advanceTimersByTimeAsync(120_000);
  expect(read).toHaveBeenCalledTimes(33);
});

it("does not let an older response overwrite post-mutation status", async () => {
  const answers: ((value: SyncOutcome<SyncStatusResult>) => void)[] = [];
  const render = setup(() => new Promise((resolve) => answers.push(resolve)));
  announceConfirmedWrite();
  answers[1]({ ok: true, result: syncStatus({ summary: "Saved just now" }) });
  await vi.advanceTimersByTimeAsync(0);
  answers[0]({ ok: true, result: syncStatus({ summary: "Old state" }) });
  await vi.advanceTimersByTimeAsync(0);
  expect(render).toHaveBeenCalledTimes(1);
  expect(render.mock.calls[0][0].result.summary).toBe("Saved just now");
});

it("stops hidden and disposed pages, then refreshes when visible", async () => {
  const read = vi.fn(async () => ({ ok: true as const, result: pendingStatus() }));
  setup(read);
  await vi.advanceTimersByTimeAsync(0);
  visibility("hidden");
  announceConfirmedWrite();
  await vi.advanceTimersByTimeAsync(20_000);
  expect(read).toHaveBeenCalledTimes(1);
  visibility("visible");
  await vi.advanceTimersByTimeAsync(0);
  expect(read).toHaveBeenCalledTimes(2);
  dispose();
  announceConfirmedWrite();
  await vi.advanceTimersByTimeAsync(20_000);
  expect(read).toHaveBeenCalledTimes(2);
});

it("never polls a refused capability", async () => {
  const read = vi.fn(async () => ({ ok: false as const, code: "forbidden", message: "unavailable" }));
  setup(read);
  await vi.advanceTimersByTimeAsync(120_000);
  expect(read).toHaveBeenCalledTimes(1);
});

it("does not keep a post-write grace timer after a capability refusal", async () => {
  const read = vi.fn(async () => ({ ok: false as const, code: "forbidden", message: "unavailable" }));
  setup(read);
  await vi.advanceTimersByTimeAsync(0);
  announceConfirmedWrite();
  await vi.advanceTimersByTimeAsync(120_000);
  expect(read).toHaveBeenCalledTimes(2);
});
