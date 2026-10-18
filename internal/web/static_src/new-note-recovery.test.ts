// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from "vitest";
import { initNewNote } from "./new-note";

const mount = (fetcher: typeof fetch, vault = "vault-a") => {
  document.body.innerHTML = `<form data-new-note data-new-note-vault="${vault}" data-create-endpoint="/api/notes/new" data-editor-path="/edit">
    <input name="title" required><input name="folder"><button type="submit">Create and write</button>
    <p data-new-note-destination></p><p data-new-note-status></p><p data-new-note-draft-status></p></form>`;
  const navigate = vi.fn();
  initNewNote(document, { fetch: fetcher, navigate });
  const form = document.querySelector("form")!;
  return {
    title: form.querySelector<HTMLInputElement>('[name="title"]')!,
    folder: form.querySelector<HTMLInputElement>('[name="folder"]')!,
    button: form.querySelector("button")!, navigate,
    input: () => form.dispatchEvent(new Event("input", { bubbles: true })),
    submit: () => form.dispatchEvent(new Event("submit", { cancelable: true })),
  };
};
const ok = () => new Response(JSON.stringify({ ok: true, key: "folder/kept" }), {
  headers: { "Content-Type": "application/json" },
});
beforeEach(() => {
  window.sessionStorage.clear();
  window.history.replaceState(null, "", "/new");
});

it("retains unsent details and a pending identity across reload, then clears them on success", async () => {
  const fetcher = vi.fn().mockRejectedValueOnce(new Error("lost")).mockResolvedValueOnce(ok());
  let page = mount(fetcher);
  page.title.value = "Kept"; page.folder.value = "Folder"; page.input();
  page = mount(fetcher);
  expect(page.title.value).toBe("Kept");
  expect(page.folder.value).toBe("Folder");
  expect(fetcher).not.toHaveBeenCalled();
  page.submit();
  await vi.waitFor(() => expect(page.button.disabled).toBe(false));
  page = mount(fetcher);
  expect(page.button.textContent).toBe("Check creation");
  expect(fetcher).toHaveBeenCalledTimes(1);
  page.submit();
  await vi.waitFor(() => expect(page.navigate).toHaveBeenCalled());
  expect(fetcher.mock.calls[0][1].body).toBe(fetcher.mock.calls[1][1].body);
  expect(mount(fetcher).title.value).toBe("");
});

it("isolates recovery by vault and retains values when storage is unavailable", () => {
  const fetcher = vi.fn();
  let page = mount(fetcher);
  page.title.value = "Private draft"; page.input();
  expect(mount(fetcher, "vault-b").title.value).toBe("");
  const storage = vi.spyOn(window, "sessionStorage", "get").mockImplementation(() => { throw new Error("blocked"); });
  page = mount(fetcher);
  page.title.value = "Still here"; page.input();
  expect(page.title.value).toBe("Still here");
  expect(document.querySelector("[data-new-note-draft-status]")?.textContent).toContain("Keep this page open");
  storage.mockRestore();
});

it("carries a valid return search to the editor and rejects external return destinations", async () => {
  const search = "/library?q=Kept&folder=Notes&status=reference";
  window.history.replaceState(null, "", `/new?${new URLSearchParams({ return_to: search })}`);
  let page = mount(vi.fn().mockResolvedValue(ok()));
  page.title.value = "Kept";
  page.submit();
  await vi.waitFor(() => expect(page.navigate).toHaveBeenCalled());
  expect(new URL(page.navigate.mock.calls[0][0], window.location.origin).searchParams.get("return_to")).toBe(search);
  window.history.replaceState(null, "", "/new?return_to=https://example.com/library");
  page = mount(vi.fn().mockResolvedValue(ok()));
  page.title.value = "Kept"; page.submit();
  await vi.waitFor(() => expect(page.navigate).toHaveBeenCalledWith("/edit?key=folder%2Fkept"));
});
