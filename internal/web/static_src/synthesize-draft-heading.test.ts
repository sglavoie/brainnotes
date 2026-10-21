import { describe, expect, it } from "vitest";

import { synthesisDraftContainsSources } from "./synthesize-model";

// The draft check mirrors noteops's CommonMark ATX matcher, so the browser
// refuses exactly the drafts the server refuses.
describe("synthesis draft Sources heading", () => {
  it.each([
    ["## Sources", true],
    ["  ## Sources ##", true],
    ["## sources\t", true],
    ["## Sources#", false],
    ["\t## Sources", false],
    ["    ## Sources", false],
    ["### Sources", false],
    ["##Sources", false],
  ])("%j -> %s", (line, want) => {
    expect(synthesisDraftContainsSources(`Intro\n\n${line}\n- a\n`)).toBe(want);
  });

  it("ignores a Sources heading inside a fence", () => {
    expect(synthesisDraftContainsSources("```\n## Sources\n```\n")).toBe(false);
  });
});
