import { expect, it } from "vitest";
import { countSourceWords, sourceBodyStart, wordCountLabel } from "./source-word-count";

it("counts source words without counting Markdown markers or splitting contractions", () => {
  expect(countSourceWords("# Café\n\n- [ ] Don't forget l’été, 42, and **déjà vu**.\n---")).toBe(8);
  expect(countSourceWords("  # > - ** _ ` ! 😃")).toBe(0);
  expect(wordCountLabel(1)).toBe("1 word");
  expect(wordCountLabel(0)).toBe("0 words");
});

it("excludes complete frontmatter with BOM, CRLF or either closing marker", () => {
  for (const close of ["---", "..."]) {
    const source = `\uFEFF---\r\ntitle: Many metadata words\r\n${close}\r\nBody words`;
    expect(countSourceWords(source.slice(sourceBodyStart(source)))).toBe(2);
  }
  expect(sourceBodyStart("---\ntitle: unfinished")).toBe(0);
  expect(sourceBodyStart("Ordinary prose\n---\nmore")).toBe(0);
});
