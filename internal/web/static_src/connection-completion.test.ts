// @vitest-environment jsdom
import { expect, it } from "vitest";
import { showSavedThought, suggestThoughtTitle } from "./connection-completion";

it("suggests a short editable filename from the first sentence without path syntax", () => {
  expect(suggestThoughtTitle("  # [[Ideas/One|Observation]] changes the question. Another thought."))
    .toBe("Observation changes the question");
  expect(suggestThoughtTitle("../unsafe: title? Next sentence")).not.toMatch(/[\\/:?]/);
  expect(suggestThoughtTitle("x".repeat(100))).toHaveLength(80);
  expect(suggestThoughtTitle("\n  ")).toBe("");
});

it("shows confirmed text literally and links to the returned key", () => {
  document.body.innerHTML = '<form><input name="title"><textarea name="body"></textarea></form>';
  const form = document.querySelector("form")!;
  form.querySelector("input")!.value = "A <literal> title";
  form.querySelector("textarea")!.value = "A thought with <script>literal text</script>.";
  showSavedThought(form, "/edit?key=Ideas%2FReturned");
  expect(form.querySelector("h3")!.textContent).toBe("A <literal> title");
  expect(form.querySelector("script")).toBeNull();
  expect(form.querySelector("a")!.getAttribute("href")).toBe("/n/Ideas/Returned");
  expect(document.activeElement).toBe(form.querySelector("h3"));
});
