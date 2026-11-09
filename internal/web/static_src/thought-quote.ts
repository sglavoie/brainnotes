// A selected rendered passage is literal prose, not new Markdown instructions.
// Escape formatting and link delimiters before nesting it in a blockquote.
export const quotedPassage = (passage: string): string =>
  passage.replace(/[\\`*_[\]<>#~|+&-]/g, "\\$&").replace(/^(\d+)([.)])(?=\s)/gm, "$1\\$2")
    .split("\n").map((line) => `> ${line}`).join("\n") + "\n\n";
