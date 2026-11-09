// Count source words, including links and code, but not YAML or punctuation.
// Iterate matches rather than allocating an array for an entire large note.
export const countSourceWords = (text: string): number => {
  let count = 0;
  const words = /[\p{L}\p{N}][\p{L}\p{M}\p{N}]*(?:['’][\p{L}\p{M}\p{N}]+)*/gu;
  while (words.exec(text) !== null) count++;
  return count;
};

export const sourceBodyStart = (text: string): number =>
  /^(?:\uFEFF)?---[ \t]*\r?\n[\s\S]*?\r?\n(?:---|\.\.\.)[ \t]*(?:\r?\n|$)/.exec(text)?.[0].length ?? 0;

export const wordCountLabel = (count: number): string => `${count.toLocaleString()} ${count === 1 ? "word" : "words"}`;
