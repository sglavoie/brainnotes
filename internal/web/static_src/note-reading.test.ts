// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { addCodeCopyControls, trackNoteSection } from "./note-reading";
import { initCopyControls } from "./clipboard";

afterEach(() => { document.body.innerHTML = ""; vi.restoreAllMocks(); vi.unstubAllGlobals(); });

it("copies highlighted code exactly and offers the existing fallback on refusal", async () => {
  const copy = vi.fn().mockResolvedValue(undefined);
  vi.stubGlobal("navigator", { clipboard: { writeText: copy } });
  document.body.innerHTML = '<article><pre><code><span>  hello &lt;world&gt;</span>\n\tend\n</code></pre></article>';
  const body = document.querySelector("article")!;
  addCodeCopyControls(body);
  addCodeCopyControls(body);
  initCopyControls();
  const button = body.querySelector("button")!;
  button.click();
  await vi.waitFor(() => expect(button.disabled).toBe(false));
  expect(copy).toHaveBeenCalledWith("  hello <world>\n\tend\n");
  expect(body.querySelectorAll("button")).toHaveLength(1);
  copy.mockRejectedValueOnce(new Error("refused"));
  button.click();
  await vi.waitFor(() => expect(body.querySelector("textarea")).not.toBeNull());
  expect(body.querySelector("textarea")!.value).toBe("  hello <world>\n\tend\n");
  expect(body.querySelector("code")!.textContent).toBe("  hello <world>\n\tend\n");
});

it("tracks sections without changing focus, URL, or history", () => {
  document.body.innerHTML = '<ol class="note-toc-list"><li><a href="#one">One</a></li><li><a href="#two">Two</a></li></ol><article><h2 id="one">One</h2><h2 id="two">Two</h2></article>';
  const body = document.querySelector("article")!;
  const links = document.querySelectorAll("a");
  links[0].focus();
  let secondTop = 400;
  vi.spyOn(document.getElementById("one")!, "getBoundingClientRect").mockImplementation(() => ({ top: -100 }) as DOMRect);
  vi.spyOn(document.getElementById("two")!, "getBoundingClientRect").mockImplementation(() => ({ top: secondTop }) as DOMRect);
  let callback: FrameRequestCallback = () => {};
  vi.spyOn(window, "requestAnimationFrame").mockImplementation((fn) => { callback = fn; return 1; });
  const url = location.href;
  const historyLength = history.length;
  const dispose = trackNoteSection(body);
  expect(links[0].getAttribute("aria-current")).toBe("location");
  secondTop = 80;
  window.dispatchEvent(new Event("scroll"));
  callback(0);
  expect(links[1].getAttribute("aria-current")).toBe("location");
  expect(links[0].hasAttribute("aria-current")).toBe(false);
  expect(document.activeElement).toBe(links[0]);
  expect(location.href).toBe(url);
  expect(history.length).toBe(historyLength);
  dispose();
});
