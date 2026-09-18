// Turning a committed attachment into the text that goes in the note.
//
// This is deliberately a module of pure functions with no DOM and no network:
// the reference a save writes into a note outlives the upload that produced it,
// and getting its encoding wrong is the one attachment bug that survives every
// retry. `attachments/Screen Shot 2026-09-18 at 09.41.22.png` is a perfectly
// ordinary destination for this server to choose — internal/attachments/name.go
// keeps spaces and replaces only what cannot travel — and an unencoded space in
// a Markdown destination ends the link at the space.

// imageExtensions are the extensions rendered inline rather than linked.
//
// The list is the one the preview can actually display, not every format a
// browser knows: an `![]()` over bytes nothing renders shows a broken-image icon
// where the person expected a file they could click.
const imageExtensions = new Set(["png", "jpg", "jpeg", "gif", "webp", "avif", "svg", "bmp", "ico"]);

export const isImageFilename = (filename: string): boolean => {
  const dot = filename.lastIndexOf(".");
  if (dot <= 0 || dot === filename.length - 1) {
    return false;
  }
  return imageExtensions.has(filename.slice(dot + 1).toLowerCase());
};

// encodeAttachmentPath percent-encodes a vault-relative path for a Markdown
// destination, one path component at a time.
//
// Two things beyond encodeURIComponent are load-bearing. The separators survive,
// because encoding them would turn one path into one filename. And the four
// characters encodeURIComponent leaves alone that Markdown does not —
// `(`, `)`, `'` and `!` — are encoded here: a closing parenthesis inside the
// destination ends the link early, which silently points the reference at a
// prefix of the file that was written.
export const encodeAttachmentPath = (relPath: string): string =>
  relPath
    .split("/")
    .map((segment) => encodeURIComponent(segment).replace(/[!'()*]/g, hexEscape))
    .join("/");

const hexEscape = (character: string): string =>
  `%${character.charCodeAt(0).toString(16).toUpperCase()}`;

// attachmentLinkText is the reference inserted at the cursor.
//
// The label is the filename the vault actually committed rather than the one the
// browser sent, because the two differ whenever a name was sanitized or
// disambiguated (`photo.png` arriving beside an existing `photo.png` is filed as
// `photo-1.png`), and a label naming a file that is not there is a reference
// nobody can follow back.
export const attachmentLinkText = (relPath: string): string => {
  const filename = relPath.slice(relPath.lastIndexOf("/") + 1);
  const destination = encodeAttachmentPath(relPath);
  const label = escapeLinkLabel(filename);
  return isImageFilename(filename) ? `![${label}](${destination})` : `[${label}](${destination})`;
};

// escapeLinkLabel protects the label's own delimiters. A filename may legally
// contain brackets — `report [final].pdf` — and an unescaped `]` closes the
// label early, leaving the rest of the name as loose text beside a broken link.
const escapeLinkLabel = (filename: string): string => filename.replace(/([[\]\\])/g, "\\$1");
