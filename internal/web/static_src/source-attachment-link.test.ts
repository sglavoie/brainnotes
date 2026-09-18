import { describe, expect, it } from "vitest";

import {
  attachmentLinkText,
  encodeAttachmentPath,
  isImageFilename,
} from "./source-attachment-link";

// The link is the one part of an attachment that outlives the upload: a wrong
// encoding survives every retry, because retrying re-uploads the file and writes
// the same broken reference again. So these are tested against the destinations
// internal/attachments/name.go actually produces — spaces kept, only what cannot
// travel replaced — rather than against tidy filenames nobody has.

describe("isImageFilename", () => {
  it("matches the extensions the preview can render, case-insensitively", () => {
    expect(isImageFilename("photo.png")).toBe(true);
    expect(isImageFilename("Screen Shot.PNG")).toBe(true);
    expect(isImageFilename("diagram.svg")).toBe(true);
    expect(isImageFilename("scan.AVIF")).toBe(true);
  });

  it("refuses anything an <img> would show as a broken icon", () => {
    expect(isImageFilename("notes.pdf")).toBe(false);
    expect(isImageFilename("archive.tar.gz")).toBe(false);
    expect(isImageFilename("clip.mp4")).toBe(false);
  });

  it("refuses a name with no usable extension", () => {
    expect(isImageFilename("README")).toBe(false);
    expect(isImageFilename("trailing.")).toBe(false);
    // A dotfile is a name that starts with a dot, not an extension-only file.
    expect(isImageFilename(".png")).toBe(false);
  });
});

describe("encodeAttachmentPath", () => {
  it("encodes a space, which would otherwise end the Markdown destination", () => {
    expect(encodeAttachmentPath("attachments/Screen Shot 2026-09-18.png")).toBe(
      "attachments/Screen%20Shot%202026-09-18.png"
    );
  });

  it("keeps the separators, so one path does not become one filename", () => {
    expect(encodeAttachmentPath("a/b/c.png")).toBe("a/b/c.png");
  });

  // encodeURIComponent leaves these five alone and Markdown does not. The
  // closing parenthesis is the dangerous one: it ends the link early and points
  // the reference at a prefix of the file that was actually written.
  it("encodes the characters encodeURIComponent leaves for Markdown to trip over", () => {
    expect(encodeAttachmentPath("attachments/report (final).pdf")).toBe(
      "attachments/report%20%28final%29.pdf"
    );
    expect(encodeAttachmentPath("it's!.png")).toBe("it%27s%21.png");
    expect(encodeAttachmentPath("star*.png")).toBe("star%2A.png");
  });

  it("encodes a non-ASCII name rather than emitting it raw", () => {
    expect(encodeAttachmentPath("attachments/café.png")).toBe("attachments/caf%C3%A9.png");
  });
});

describe("attachmentLinkText", () => {
  it("embeds an image and links anything else", () => {
    expect(attachmentLinkText("attachments/photo.png")).toBe(
      "![photo.png](attachments/photo.png)"
    );
    expect(attachmentLinkText("attachments/notes.pdf")).toBe(
      "[notes.pdf](attachments/notes.pdf)"
    );
  });

  it("labels with the committed filename, not the whole path", () => {
    expect(attachmentLinkText("a/b/Screen Shot.png")).toBe(
      "![Screen Shot.png](a/b/Screen%20Shot.png)"
    );
  });

  it("escapes the label's own delimiters", () => {
    expect(attachmentLinkText("report [final].pdf")).toBe(
      "[report \\[final\\].pdf](report%20%5Bfinal%5D.pdf)"
    );
  });

  it("handles a destination with no directory at all", () => {
    expect(attachmentLinkText("photo.png")).toBe("![photo.png](photo.png)");
  });
});
