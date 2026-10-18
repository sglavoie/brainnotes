export type SearchResult = {
  position?: number;
  key: string;
  path: string;
  title: string;
  // wikilink is the text a link to this note carries between the brackets. It
  // cannot be derived from the fields beside it — `key` is folded to lower case
  // and `title` is whatever the frontmatter says, while the graph resolves the
  // exact-case vault path — so the server publishes it. The raw-Markdown
  // editor's `[[` completion is what needs it.
  wikilink: string;
  folder: string;
  // The same frontmatter `last` timestamp used by sort=modified, when valid.
  modified?: string;
  matched_fields: string[];
  issues: string[];
  snippet: string;
  pinned: boolean;
};
