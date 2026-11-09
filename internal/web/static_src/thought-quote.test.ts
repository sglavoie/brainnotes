import { expect, it } from "vitest";
import { quotedPassage } from "./thought-quote";

it("keeps every line of a selected passage inside the quotation", () => {
  expect(quotedPassage("First paragraph.\n\nSecond paragraph."))
    .toBe("> First paragraph.\n> \n> Second paragraph.\n\n");
});

it("preserves literal formatting, HTML and wikilink text without introducing links", () => {
  expect(quotedPassage("Use depth_cm and [[Unintended link]], <script>, `code`, *stars* and \\.")).toBe(
    "> Use depth\\_cm and \\[\\[Unintended link\\]\\], \\<script\\>, \\`code\\`, \\*stars\\* and \\\\.\n\n",
  );
});

it("keeps heading, list and entity syntax literal inside a quotation", () => {
  expect(quotedPassage("# Heading\n- item\n1. item\n&copy;"))
    .toBe("> \\# Heading\n> \\- item\n> 1\\. item\n> \\&copy;\n\n");
});
