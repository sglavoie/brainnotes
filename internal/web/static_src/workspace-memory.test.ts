// @vitest-environment jsdom
import { beforeEach, afterEach, expect, it, vi } from "vitest";
import { initWorkspaceMemory, readWorkspaceMemory, writeWorkspaceMemory } from "./workspace-memory";

beforeEach(() => {
  localStorage.clear(); sessionStorage.clear();
  document.documentElement.dataset.vaultId = "one";
  document.body.innerHTML = '<div><button data-forget-workspace>Forget</button><p role="status"></p></div>';
});
afterEach(() => vi.restoreAllMocks());

it("keeps a newer session fallback readable when durable storage fills up", () => {
  writeWorkspaceMemory("reference", "Old reference");
  const original = Storage.prototype.setItem;
  vi.spyOn(Storage.prototype, "setItem").mockImplementation(function (this: Storage, key, value) {
    if (this === localStorage) throw new Error("quota");
    original.call(this, key, value);
  });
  expect(writeWorkspaceMemory("reference", "New reference")).toBe(true);
  expect(readWorkspaceMemory("reference")).toBe("New reference");
  document.documentElement.dataset.vaultId = "two";
  expect(readWorkspaceMemory("reference")).toBeNull();
});

it("forgets only this vault's reading context, preserving drafts and other vaults", () => {
  localStorage.setItem("bn.resume.v1:two", "other vault");
  sessionStorage.setItem("bn.new-note.v1:draft", "unsaved words");
  for (const name of ["resume.v1", "reference", "switcher.recent"] as const) writeWorkspaceMemory(name, "remembered");
  initWorkspaceMemory(); document.querySelector("button")!.click();
  expect(readWorkspaceMemory("reference")).toBeNull();
  expect(readWorkspaceMemory("resume.v1")).toBeNull();
  expect(readWorkspaceMemory("switcher.recent")).toBeNull();
  expect(localStorage.getItem("bn.resume.v1:two")).toBe("other vault");
  expect(sessionStorage.getItem("bn.new-note.v1:draft")).toBe("unsaved words");
  expect(document.querySelector("[role=status]")!.textContent).toContain("cleared");
});

it("does not claim to have cleared inaccessible storage", () => {
  vi.spyOn(Storage.prototype, "removeItem").mockImplementation(() => { throw new Error("blocked"); });
  initWorkspaceMemory(); document.querySelector("button")!.click();
  expect(document.querySelector("[role=status]")!.textContent).toContain("could not be fully cleared");
});
