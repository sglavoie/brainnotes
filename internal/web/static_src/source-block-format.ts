import type { SourceEdit, SourceSelection } from "./source-format";

type Block = "bullet" | "numbered" | "task" | "quote";
const prefixes: Record<Block, RegExp> = {
  bullet: /^[-*+] (?!\[[ xX]\] )/,
  numbered: /^\d+[.)] /,
  task: /^[-*+] \[[ xX]\] /,
  quote: /^> ?/,
};
const listPrefix = /^(?:[-*+] (?:\[[ xX]\] )?|\d+[.)] )/;
export const isBlockFormat = (command: string): command is Block => Object.hasOwn(prefixes, command);

// Convert list kinds instead of stacking markers. Removing a quote removes
// exactly one level; indentation and checked tasks survive repeated formatting.
export const formatBlock = (doc: string, span: SourceSelection, kind: Block): SourceEdit => {
  const lines = doc.slice(span.from, span.to).split("\n");
  const remove = lines.some((line) => line.trim() !== "") &&
    lines.every((line) => line.trim() === "" || prefixes[kind].test(line.trimStart()));
  const numbers = new Map<number, number>();
  const insert = lines.map((line) => {
    if (line.trim() === "" && lines.length > 1) return line;
    const indent = /^[\t ]*/.exec(line)![0];
    const body = line.slice(indent.length);
    if (remove) return indent + body.replace(prefixes[kind], "");
    if (kind !== "numbered" && prefixes[kind].test(body)) return line;
    const marker = blockMarker(kind, indent.length, numbers);
    return indent + marker + (kind === "quote" ? body : body.replace(listPrefix, ""));
  }).join("\n");
  const end = span.from + insert.length;
  return { ...span, insert, selectionFrom: doc.slice(span.from, span.to).trim() === "" ? end : span.from, selectionTo: end };
};

const blockMarker = (kind: Block, depth: number, numbers: Map<number, number>): string => {
  if (kind !== "numbered") return { bullet: "- ", task: "- [ ] ", quote: "> " }[kind];
  for (const level of numbers.keys()) if (level > depth) numbers.delete(level);
  const next = (numbers.get(depth) ?? 0) + 1;
  numbers.set(depth, next);
  return `${next}. `;
};
