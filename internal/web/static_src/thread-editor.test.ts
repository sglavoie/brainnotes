// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from "vitest";
import { initThreadEditor } from "./thread-editor";
import type { SourceEditorController } from "./source-editor-controller";

const item = { group: "Evidence", title: "Alpha", key: "alpha", path: "/n/alpha", quote: "Literal <script>quotation</script>", commentary: "My reason", warning: "" };
const response = (content: string) => new Response(JSON.stringify({ content, items: [item] }), { headers: { "Content-Type": "application/json" } });

beforeEach(() => { document.body.innerHTML = '<section data-thread-editor><aside data-thread-material></aside></section>'; });

it("does not replace writing typed while an arrangement is being prepared", async () => {
  let content = "original";
  let finish!: (value: Response) => void;
  const fetcher = vi.fn().mockResolvedValueOnce(response(content)).mockImplementationOnce(() => new Promise<Response>((resolve) => { finish = resolve; }));
  vi.stubGlobal("fetch", fetcher);
  const replace = vi.fn((before: string, after: string) => { if (content !== before) return false; content = after; return true; });
  const editor = { content: () => content, replaceContent: replace } as unknown as SourceEditorController;
  initThreadEditor(document.querySelector("section")!, editor, "token", "thread");
  await vi.waitFor(() => expect(document.querySelector("article")).not.toBeNull());
  expect(document.querySelector("blockquote")?.textContent).toBe(item.quote);
  expect(document.querySelector("script")).toBeNull();
  const select = document.querySelector("select")!;
  select.value = "Still unclear"; select.dispatchEvent(new Event("change"));
  await vi.waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2));
  content = "new writing"; finish(response("rearranged old writing"));
  await vi.waitFor(() => expect(document.querySelector('[role="status"]')?.textContent).toContain("Your text is kept"));
  expect(content).toBe("new writing");
  vi.unstubAllGlobals();
});

it("keeps commentary controls intact when collection text changed in the editor", async () => {
  let content = "original collection";
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response(content)));
  const editor = { content: () => content, replaceContent: vi.fn() } as unknown as SourceEditorController;
  initThreadEditor(document.querySelector("section")!, editor, "token", "thread");
  await vi.waitFor(() => expect(document.querySelector("article")).not.toBeNull());
  const writing = document.querySelector("textarea")!;
  writing.value = "An explanation I have not saved";
  content = "externally arranged collection";
  const update = [...document.querySelectorAll("button")].find((button) => button.textContent === "Update commentary")!;
  update.click();
  expect(document.querySelector("textarea")).toBe(writing);
  expect(writing.value).toBe("An explanation I have not saved");
  expect(document.querySelector('[role="status"]')?.textContent).toContain("Your commentary is kept");
  vi.unstubAllGlobals();
});

it("arranges the current document after writing outside the collection", async () => {
  const collection = "<!-- bn:thread:v1 -->\ncollection\n<!-- bn:thread:end -->";
  let content = "Question\n" + collection + "\nDraft";
  const fetcher = vi.fn().mockResolvedValueOnce(response(content)).mockImplementationOnce((_url, init) => {
    const submitted = (init.body as URLSearchParams).get("content")!;
    return Promise.resolve(response(submitted.replace("collection", "arranged collection")));
  });
  vi.stubGlobal("fetch", fetcher);
  const replace = vi.fn((before: string, after: string) => { expect(before).toBe(content); content = after; return true; });
  const editor = { content: () => content, replaceContent: replace } as unknown as SourceEditorController;
  initThreadEditor(document.querySelector("section")!, editor, "token", "thread");
  await vi.waitFor(() => expect(document.querySelector("article")).not.toBeNull());
  content += " with new writing";
  const select = document.querySelector("select")!;
  select.value = "Still unclear"; select.dispatchEvent(new Event("change"));
  await vi.waitFor(() => expect(replace).toHaveBeenCalled());
  expect(content).toContain("Draft with new writing");
  vi.unstubAllGlobals();
});
