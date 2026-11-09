const excluded = '.note-section-copy, .note-code-copy, [data-passage-annotation], .passage-annotation-group, script, style';
const blockTags = /^(P|LI|BLOCKQUOTE|H[1-6]|PRE|TR|DIV)$/;
const normalize = (value: string): string => value.normalize('NFC').replace(/\s+/gu, ' ').trim();

// Index visible prose once, preserving inline joins and block boundaries. Each
// character points to its source block; controls and saved thoughts aren't prose.
export const passageIndex = (body: HTMLElement): { text: string; blocks: HTMLElement[] } => {
  let text = '';
  const blocks: HTMLElement[] = [];
  const append = (value: string, block: HTMLElement): void => {
    for (const char of value.normalize('NFC')) {
      const space = /\s/u.test(char);
      if (space && (!text || text.endsWith(' '))) continue;
      const next = space ? ' ' : char;
      text += next;
      for (let i = 0; i < next.length; i++) blocks.push(block);
    }
  };
  const visit = (node: Node, block: HTMLElement): void => {
    if (node instanceof HTMLElement) {
      if (node.matches(excluded)) return;
      if (node.matches('[data-source-line]')) block = node;
      if (blockTags.test(node.tagName) || node.tagName === 'BR') append(' ', block);
      for (const child of node.childNodes) visit(child, block);
      if (blockTags.test(node.tagName)) append(' ', block);
    } else if (node.nodeType === Node.TEXT_NODE) append(node.textContent || '', block);
  };
  visit(body, body);
  return { text, blocks };
};

export const anchorPassage = (index: ReturnType<typeof passageIndex>, quote: string): HTMLElement | null => {
  const wanted = normalize(quote);
  if (!wanted) return null;
  const start = index.text.indexOf(wanted);
  if (start < 0 || index.text.indexOf(wanted, start + 1) >= 0) return null;
  return index.blocks[start + wanted.length - 1] || null;
};
