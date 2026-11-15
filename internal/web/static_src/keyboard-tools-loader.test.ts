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

it("keeps an existing dialog in charge without requesting a bundle", async () => {
  document.body.innerHTML = '<div role="dialog"></div>';
  await openKeyboardTools("sheet");
  expect(loadWorkspaceScript).not.toHaveBeenCalled();
});
