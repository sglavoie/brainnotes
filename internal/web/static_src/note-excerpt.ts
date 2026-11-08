// Excerpts are plain text. Strip wikilink syntax without interpreting HTML or
// constructing routes from a potentially ambiguous short note title.
export const readableExcerpt = (text: string): string => text.replace(
  /!?\[\[([^\]\n]+)\]\]/g,
  (_whole, target: string) => {
    const [path, alias] = target.split("|");
    return alias?.trim() || path.split("#")[0].replace(/\.md$/i, "").split("/").pop() || path;
  }
);
