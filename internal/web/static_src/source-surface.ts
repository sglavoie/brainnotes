import type { SourceEdit, SourceSelection } from "./source-format";

// What the editor controller needs from a text surface, and nothing more.
//
// The only production implementation is the CodeMirror adapter in
// source-editor-view.ts. The interface exists because CodeMirror measures a
// real layout — it needs a viewport, scroll geometry and a font — and under
// jsdom it either refuses to render or renders something no assertion can trust.
// Everything that decides *what the editor does* is therefore written against
// this interface and tested in Vitest; only "CodeMirror is wired up
// correctly" is left to the browser suite, where it is one page load rather
// than a whole test strategy.
//
// Keep this limited to operations the controller actually calls.

export type SourceSurface = {
  getValue(): string;
  // setValue replaces the document. The selection after it is the surface's
  // business; nothing the controller does depends on where it lands.
  setValue(value: string): void;
  getSelection(): SourceSelection;
  applyEdit(edit: SourceEdit): void;
  focus(): void;
  openSearch(): void;
  // onChange fires for a user edit. A setValue performed by the controller
  // must not fire it, or loading a note would mark it dirty.
  onChange(listener: () => void): void;
  destroy(): void;
};

// SourceSurfaceFactory builds the surface over a host element. The controller
// takes one rather than a surface so the host stays hidden until there is a
// document to put in it.
export type SourceSurfaceFactory = (host: HTMLElement, initialValue: string) => SourceSurface;
