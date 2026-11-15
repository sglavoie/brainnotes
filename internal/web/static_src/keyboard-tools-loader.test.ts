// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { loadWorkspaceScript } from "./workspace-script";
import { openKeyboardTools } from "./keyboard-tools-loader";

vi.mock("./workspace-script", () => ({ loadWorkspaceScript: vi.fn() }));
afterEach(() => { document.body.innerHTML = ""; vi.clearAllMocks(); });

it("does not steal focus from typing that began while the bundle loaded", async () => {
  let ready = () => {};
  vi.mocked(loadWorkspaceScript).mockImplementationOnce(() => new Promise<void>((resolve) => { ready = resolve; }));
  const opened = vi.fn();
  document.addEventListener("bn:keyboard-tools", opened);
  document.body.innerHTML = '<button>Search</button><input>';
  document.querySelector("button")!.focus();
  const request = openKeyboardTools("switcher");
  await openKeyboardTools("switcher");
  expect(loadWorkspaceScript).toHaveBeenCalledTimes(1);
  document.querySelector("input")!.focus();
  ready();
  await request;
  expect(opened).not.toHaveBeenCalled();
  document.removeEventListener("bn:keyboard-tools", opened);
});

it("preserves the original focused control and requested tool", async () => {
  vi.mocked(loadWorkspaceScript).mockResolvedValueOnce();
  document.body.innerHTML = '<button>Help</button>';
  const origin = document.querySelector("button")!;
  origin.focus();
  const opened = vi.fn();
  document.addEventListener("bn:keyboard-tools", opened);
  await openKeyboardTools("sheet");
  expect((opened.mock.calls[0][0] as CustomEvent).detail).toEqual({ action: "sheet", origin });
  document.removeEventListener("bn:keyboard-tools", opened);
});

it.each(["sheet", "switcher"] as const)("keeps an existing native dialog in charge when requesting %s", async (action) => {
  document.body.innerHTML = '<dialog open></dialog>';
  await openKeyboardTools(action);
  expect(loadWorkspaceScript).not.toHaveBeenCalled();
});

it.each(["sheet", "switcher"] as const)("opens %s over a nonmodal preview", async (action) => {
  vi.mocked(loadWorkspaceScript).mockResolvedValueOnce();
  document.body.innerHTML = '<div role="dialog" popover="manual"></div><dialog></dialog>';
  const opened = vi.fn();
  document.addEventListener("bn:keyboard-tools", opened);
  await openKeyboardTools(action);
  expect(loadWorkspaceScript).toHaveBeenCalledTimes(1);
  expect(opened).toHaveBeenCalledTimes(1);
  expect((opened.mock.calls[0][0] as CustomEvent).detail.action).toBe(action);
  document.removeEventListener("bn:keyboard-tools", opened);
});

it.each(["sheet", "switcher"] as const)("yields when a native dialog opens while loading %s", async (action) => {
  let ready = () => {};
  vi.mocked(loadWorkspaceScript).mockImplementationOnce(() => new Promise<void>((resolve) => { ready = resolve; }));
  const opened = vi.fn();
  document.addEventListener("bn:keyboard-tools", opened);
  const request = openKeyboardTools(action);
  document.body.innerHTML = '<dialog open></dialog>';
  ready();
  await request;
  expect(opened).not.toHaveBeenCalled();
  document.removeEventListener("bn:keyboard-tools", opened);
});
