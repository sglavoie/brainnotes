// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from "vitest";
import { initNewNote } from "./new-note";

const response = (payload: unknown, status = 200): Response => new Response(JSON.stringify(payload), {
  status, headers: { "Content-Type": "application/json" },
});

const setup = (fetcher: typeof fetch) => {
  const navigate = vi.fn();
  initNewNote(document, { fetch: fetcher, navigate });
  const form = document.querySelector("form")!;
  const title = document.querySelector<HTMLInputElement>('[name="title"]')!;
  const folder = document.querySelector<HTMLInputElement>('[name="folder"]')!;
  const button = document.querySelector("button")!;
  const submit = () => form.dispatchEvent(new Event("submit", { cancelable: true }));
  return { navigate, form, title, folder, button, submit };
};

beforeEach(() => {
  document.body.innerHTML = `<form data-new-note data-create-endpoint="/api/notes/new" data-editor-path="/edit" data-csrf="token">
    <input name="title" required value="Fresh note"><input name="folder" value="Ideas">
    <button type="submit">Create and write</button>
    <p data-new-note-destination></p><p data-new-note-status></p>
  </form>`;
});

describe("new note", () => {
  it("previews the destination and opens the server's returned key", async () => {
    const fetcher = vi.fn().mockResolvedValue(response({ ok: true, key: "ideas/fresh note" }));
    const h = setup(fetcher);
    expect(document.querySelector("[data-new-note-destination]")?.textContent).toBe("Destination: Ideas/Fresh note.md");
    h.submit();
    await vi.waitFor(() => expect(h.navigate).toHaveBeenCalledWith("/edit?key=ideas%2Ffresh%20note"));
    const body = new URLSearchParams(fetcher.mock.calls[0][1].body);
    expect(body.get("title")).toBe("Fresh note");
    expect(body.get("folder")).toBe("Ideas");
    expect(body.get("save_id")).toBeTruthy();
    expect(fetcher.mock.calls[0][1].headers["X-BN-CSRF"]).toBe("token");
  });

  it("blocks duplicate submissions and retries a lost response with the same id", async () => {
    const fetcher = vi.fn().mockRejectedValueOnce(new Error("lost response"))
      .mockResolvedValue(response({ ok: true, key: "fresh" }));
    const h = setup(fetcher);
    h.submit(); h.submit();
    expect(fetcher).toHaveBeenCalledTimes(1);
    await vi.waitFor(() => expect(h.button.disabled).toBe(false));
    expect(h.title.value).toBe("Fresh note");
    expect(document.querySelector("[data-new-note-status]")?.textContent).toContain("Creation not confirmed");
    h.submit();
    await vi.waitFor(() => expect(h.navigate).toHaveBeenCalled());
    expect(fetcher.mock.calls[0][1].body).toBe(fetcher.mock.calls[1][1].body);
  });

  it("keeps input on a refusal and changes the id when the destination changes", async () => {
    const fetcher = vi.fn().mockImplementation(() => Promise.resolve(response({ ok: false, message: "Already exists" }, 409)));
    const h = setup(fetcher);
    h.submit();
    await vi.waitFor(() => expect(h.button.disabled).toBe(false));
    expect(h.title.value).toBe("Fresh note");
    h.title.value = "Different";
    h.submit();
    await vi.waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2));
    const ids = fetcher.mock.calls.map((call) => new URLSearchParams(call[1].body).get("save_id"));
    expect(ids[0]).not.toBe(ids[1]);
    expect(h.navigate).not.toHaveBeenCalled();
  });

  it("keeps a post-write warning visible and offers the created note without resubmitting", async () => {
    const fetcher = vi.fn().mockResolvedValue(response({ ok: false, changed: true, key: "ideas/fresh", message: "Written, but history failed." }, 500));
    const h = setup(fetcher);
    h.submit();
    await vi.waitFor(() => expect(document.querySelector("[data-new-note-status] a")).not.toBeNull());
    expect(document.querySelector("[data-new-note-status]")?.textContent).toContain("history failed");
    expect(document.querySelector("a")?.getAttribute("href")).toBe("/edit?key=ideas%2Ffresh");
    expect(h.button.disabled).toBe(true);
    h.submit();
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(h.navigate).not.toHaveBeenCalled();
  });
});
