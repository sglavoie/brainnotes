// @vitest-environment jsdom
import { beforeEach, expect, it } from "vitest";
import { briefEditorDestination, readNoteResume, rememberNoteResume, resumeDestination } from "./note-resume";
import { readableExcerpt } from "./note-excerpt";

beforeEach(() => { sessionStorage.clear(); document.documentElement.dataset.vaultId = "vault-one"; });

it("keeps passage hints scoped to the vault and tab, bounded to recent notes", () => {
  const position = { sourceLine: 10, heading: "section", label: "A heading", excerpt: "words", offset: 0, ratio: 0.2 };
  rememberNoteResume("Folder/Été.md", position);
  expect(readNoteResume("folder/été")).toEqual(position);
  expect(new URL(resumeDestination("/edit?key=folder%2F%C3%A9t%C3%A9"), location.origin).searchParams.get("read_at")).toBe(JSON.stringify(position));
  document.documentElement.dataset.vaultId = "vault-two";
  expect(readNoteResume("folder/été")).toBeNull();
  document.documentElement.dataset.vaultId = "vault-one";
  for (let i = 0; i < 32; i++) rememberNoteResume(`note-${i}`, position);
  expect(readNoteResume("folder/été")).toBeNull();
  expect(readNoteResume("note-31")).toEqual(position);
});

it("constructs only an editor route and rejects arbitrary navigation", () => {
  expect(briefEditorDestination("/edit?key=a%26b&extra=ignored")).toBe("/edit?key=a%26b");
  for (const target of ["https://elsewhere.test/edit?key=x", "//elsewhere.test/edit?key=x", "/edit", "/edit?key=", "javascript:alert(1)"]) {
    expect(briefEditorDestination(target)).toBe("");
  }
});

it("reads wiki aliases without treating excerpt text as markup", () => {
  expect(readableExcerpt("See [[Books/Deep Work#Focus|the book]] and [[Ideas.md]]. <img src=x>"))
    .toBe("See the book and Ideas. <img src=x>");
});
