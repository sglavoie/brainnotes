// Text nodes only: note content must never become HTML in previews or search.
// Search's wire format is Go-escaped text plus exact <mark> tags. Decode only
// that vocabulary before constructing our own text nodes and highlights.
export const searchExcerptText = (snippet: string): string => {
  const entities: Record<string, string> = { "&amp;": "&", "&lt;": "<", "&gt;": ">", "&#39;": "'", "&#34;": '"' };
  return snippet.replace(/<\/?mark>/g, "").replace(/&(amp|lt|gt|#39|#34);/g, (entity) => entities[entity]);
};

export const highlightedText = (text: string, terms: string[]): DocumentFragment => {
  const fragment = document.createDocumentFragment();
  const words = terms.map((term) => term.trim()).filter(Boolean).sort((a, b) => b.length - a.length);
  if (!words.length) { fragment.append(text); return fragment; }
  const pattern = new RegExp(words.map((word) => word.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")).join("|"), "giu");
  let offset = 0;
  for (const match of text.matchAll(pattern)) {
    fragment.append(text.slice(offset, match.index));
    const mark = document.createElement("mark");
    mark.textContent = match[0]; fragment.append(mark);
    offset = match.index + match[0].length;
  }
  fragment.append(text.slice(offset));
  return fragment;
};
