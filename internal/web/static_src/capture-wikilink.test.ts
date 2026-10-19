// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { CaptureWikilinks } from "./capture-wikilink";

const response = (targets = ["People/Bob", "Books/Bobby"]): Response =>
  new Response(JSON.stringify(targets.map((wikilink) => ({ wikilink }))));

const setup = (fetcher = vi.fn().mockResolvedValue(response())) => {
  document.body.innerHTML = '<textarea maxlength="2000"></textarea>';
  const input = document.querySelector("textarea")!;
  const completion = new CaptureWikilinks(input, fetcher);
  input.focus();
  return { input, completion, fetcher };
};

const type = (input: HTMLTextAreaElement, value: string, caret = value.length): void => {
  input.value = value;
  input.setSelectionRange(caret, caret);
  input.dispatchEvent(new Event("input"));
};

const key = (input: HTMLTextAreaElement, name: string, options: KeyboardEventInit = {}): KeyboardEvent => {
  const event = new KeyboardEvent("keydown", { key: name, cancelable: true, ...options });
  input.dispatchEvent(event);
  return event;
};

beforeEach(() => vi.useFakeTimers());
afterEach(() => { vi.clearAllTimers(); vi.useRealTimers(); document.body.replaceChildren(); });

describe("capture wikilinks", () => {
  it("inserts an exact target with keyboard selection and preserves surrounding text", async () => {
    const { input, fetcher } = setup();
    type(input, "Ask [[Bo tomorrow", 8);
    await vi.advanceTimersByTimeAsync(150);
    expect(fetcher.mock.calls[0][0]).toBe("/api/search?q=Bo&limit=12");
    expect(input.getAttribute("role")).toBe("combobox");
    expect(input.getAttribute("aria-expanded")).toBe("true");
    key(input, "ArrowDown");
    const changed = vi.fn();
    input.addEventListener("input", changed);
    expect(key(input, "Enter").defaultPrevented).toBe(true);
    expect(input.value).toBe("Ask [[Books/Bobby]] tomorrow");
    expect(changed).toHaveBeenCalledOnce();
    expect(input.selectionStart).toBe("Ask [[Books/Bobby]]".length);
    expect(input.hasAttribute("aria-activedescendant")).toBe(false);
    expect(input.getAttribute("aria-expanded")).toBe("false");
  });

  it("supports clicking without doubling closing brackets", async () => {
    const { input } = setup();
    type(input, "[[Bo]]", 4);
    await vi.advanceTimersByTimeAsync(150);
    const row = document.querySelector<HTMLElement>('[role="option"]')!;
    const mouse = new MouseEvent("mousedown", { bubbles: true, cancelable: true });
    row.dispatchEvent(mouse);
    expect(mouse.defaultPrevented).toBe(true);
    row.click();
    expect(input.value).toBe("[[People/Bob]]");
    expect(input.selectionStart).toBe(input.value.length);
    expect(document.activeElement).toBe(input);
  });

  it("ignores stale replies, including after Escape", async () => {
    let finish!: (value: Response) => void;
    const fetcher = vi.fn().mockImplementationOnce(() => new Promise<Response>((resolve) => { finish = resolve; }))
      .mockResolvedValue(response(["New"]));
    const { input } = setup(fetcher);
    type(input, "[[Old");
    await vi.advanceTimersByTimeAsync(150);
    type(input, "[[New");
    await vi.advanceTimersByTimeAsync(150);
    expect(document.querySelector('[role="option"]')?.textContent).toBe("New");
    key(input, "Escape");
    finish(response(["Old"]));
    await vi.advanceTimersByTimeAsync(0);
    expect(document.querySelector<HTMLUListElement>("ul")!.hidden).toBe(true);
  });

  it("leaves capture submission and IME Enter alone", async () => {
    const { input } = setup();
    type(input, "[[Bo");
    await vi.advanceTimersByTimeAsync(150);
    expect(key(input, "Enter", { ctrlKey: true }).defaultPrevented).toBe(false);
    expect(key(input, "Enter", { metaKey: true }).defaultPrevented).toBe(false);
    expect(key(input, "Enter", { isComposing: true }).defaultPrevented).toBe(false);
    expect(input.value).toBe("[[Bo");
  });

  it("respects maxlength and keeps the draft on a search failure", async () => {
    const { input, fetcher } = setup();
    input.maxLength = 6;
    type(input, "[[Bo");
    await vi.advanceTimersByTimeAsync(150);
    key(input, "Enter");
    expect(input.value).toBe("[[Bo");
    fetcher.mockRejectedValueOnce(new Error("offline"));
    type(input, "[[Bob");
    await vi.advanceTimersByTimeAsync(150);
    expect(input.value).toBe("[[Bob");
    expect(document.querySelector<HTMLUListElement>("ul")!.hidden).toBe(true);
  });

  it("does not search plain text, completed links, aliases, headings, or selected text", async () => {
    const { input, fetcher } = setup();
    for (const text of ["plain", "[[Bob]]", "[[Bob|alias", "[[Bob#heading"]) {
      type(input, text);
      await vi.advanceTimersByTimeAsync(150);
    }
    type(input, "[[Bo");
    input.setSelectionRange(2, 4);
    await vi.advanceTimersByTimeAsync(150);
    expect(fetcher).not.toHaveBeenCalled();
  });

  it("cancels pending suggestions on blur or capture submission", async () => {
    const { input, completion, fetcher } = setup();
    type(input, "[[Bo");
    input.blur();
    await vi.advanceTimersByTimeAsync(150);
    expect(fetcher).not.toHaveBeenCalled();
    type(input, "[[Bo");
    completion.close();
    await vi.advanceTimersByTimeAsync(150);
    expect(fetcher).not.toHaveBeenCalled();
  });
});
