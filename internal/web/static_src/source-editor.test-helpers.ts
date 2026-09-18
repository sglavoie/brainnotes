import type { SourceSelection } from "./source-format";
import type { SourceSurface, SourceSurfaceFactory } from "./source-surface";

// Fixtures for the editor's jsdom tests.
//
// The markup below is a second expression of internal/web/templates/editor.html
// and is deliberately so: a Node process cannot render a Go template. The
// *attribute names* are the contract between them, and they are pinned on the
// Go side by internal/web/editor_page_test.go, which asserts the rendered page
// carries every one of them. If this fixture drifts, that test still fails on
// the real page rather than this one passing against markup nobody serves.

export const editorMarkup = (): string => `
<section
  class="panel source-editor"
  data-source-editor
  data-source-key="alpha"
  data-source-vault-id="sync:vault-under-test"
  data-source-csrf="csrf-token"
  data-source-rel-path="Alpha.md"
  data-source-read-endpoint="/api/source"
  data-source-preview-endpoint="/api/source/preview"
  data-source-save-endpoint="/api/source/save"
  data-source-create-endpoint="/api/source/create"
  data-source-search-endpoint="/api/search"
  data-source-max-bytes="1048576"
  data-source-upload-endpoint="/api/attachments/upload"
  data-source-upload-cancel-endpoint="/api/attachments/cancel"
  data-source-max-attachment-bytes="26214400"
  data-source-editor-path="/edit"
  data-source-home-path="/"
  data-source-capture-endpoint="/api/capture"
  data-source-note-create-endpoint="/api/notes/create"
  data-source-rename-endpoint="/api/notes/rename"
  data-source-move-endpoint="/api/notes/move"
  data-source-trash-endpoint="/api/notes/trash"
  data-source-frontmatter-set-endpoint="/api/notes/frontmatter/set"
  data-source-frontmatter-unset-endpoint="/api/notes/frontmatter/unset"
  data-source-pin-set-endpoint="/api/pins/set"
  data-source-pin-remove-endpoint="/api/pins/remove"
  data-source-undo-endpoint="/api/undo"
>
  <div class="source-editor-toolbar">
    <button type="button" data-source-command="bold">Bold</button>
    <button type="button" data-source-command="wikilink">Wikilink</button>
    <button type="button" data-source-save disabled>Save</button>
  </div>
  <p data-source-status></p>
  <p data-source-draft-status></p>
  <section data-source-recovery hidden>
    <h3 data-source-recovery-heading></h3>
    <p data-source-recovery-lede></p>
    <pre data-source-recovery-source></pre>
    <button type="button" data-source-recovery-restore>Put it back in the editor</button>
    <button type="button" data-source-recovery-recreate hidden>Save it as a new note</button>
    <button type="button" data-source-recovery-discard>Discard it</button>
  </section>
  <div class="source-editor-panes" data-source-panes>
    <div class="source-editor-tabs" role="tablist">
      <button type="button" role="tab" data-source-tab="edit" aria-selected="true">Edit</button>
      <button type="button" role="tab" data-source-tab="preview" aria-selected="false" tabindex="-1">Preview</button>
    </div>
    <div class="source-editor-pane source-editor-pane-edit" data-source-pane="edit">
      <div data-source-surface></div>
    </div>
    <div class="source-editor-pane source-editor-pane-preview" data-source-pane="preview">
      <p data-source-preview-status></p>
      <div class="note-body" data-source-preview></div>
    </div>
  </div>
  <section data-source-conflict hidden>
    <pre data-source-conflict-source></pre>
    <button type="button" data-source-conflict-overwrite>Save mine over it</button>
    <button type="button" data-source-conflict-discard>Discard mine</button>
  </section>
  <div class="source-editor-attach">
    <input type="file" data-source-attachment-input multiple hidden>
    <button type="button" data-source-attachment-choose>Attach files…</button>
  </div>
  <section data-source-attachments hidden>
    <ul data-source-attachment-list></ul>
  </section>
  <details data-source-manage>
    <summary>Manage this note</summary>
    <p data-source-manage-status></p>
    <section data-source-gate hidden>
      <p data-source-gate-message></p>
      <button type="button" data-source-gate-save>Save first, then continue</button>
      <button type="button" data-source-gate-proceed hidden>Continue anyway</button>
      <button type="button" data-source-gate-cancel>Cancel</button>
    </section>
    <input type="text" data-source-rename-title>
    <button type="button" data-source-rename>Rename</button>
    <input type="text" data-source-move-folder>
    <button type="button" data-source-move>Move</button>
    <button type="button" data-source-trash>Move to Trash</button>
    <input type="text" data-source-property-name>
    <input type="text" data-source-property-value>
    <button type="button" data-source-property-set>Set</button>
    <button type="button" data-source-property-unset>Remove</button>
    <button type="button" data-source-pin>Pin this note</button>
    <button type="button" data-source-unpin>Unpin this note</button>
    <textarea data-source-capture-text rows="2"></textarea>
    <button type="button" data-source-capture>Capture</button>
    <input type="text" data-source-note-create-target>
    <button type="button" data-source-note-create>Create</button>
    <button type="button" data-source-undo>Undo the last change</button>
  </details>
</section>
`;

export type FakeSurface = SourceSurface & {
  // type simulates a user edit: it fires the change listeners, which a
  // controller-driven setValue must not.
  type(value: string): void;
  // select moves the cursor without editing anything, so a test can say where
  // the person is before asking the controller to insert there.
  select(from: number, to: number): void;
  destroyed(): boolean;
};

// createFakeSurface is a plain string with a selection. It is enough for every
// decision the controller makes, which is the point of SourceSurface existing:
// CodeMirror needs a real layout and jsdom cannot give it one.
export const createFakeSurfaceFactory = (): {
  factory: SourceSurfaceFactory;
  surface(): FakeSurface | null;
} => {
  let built: FakeSurface | null = null;

  const factory: SourceSurfaceFactory = (_host, initialValue) => {
    let value = initialValue;
    let selection: SourceSelection = { from: 0, to: 0 };
    let destroyed = false;
    const listeners: (() => void)[] = [];
    const notify = () => {
      for (const listener of listeners) {
        listener();
      }
    };

    built = {
      getValue: () => value,
      setValue(next: string): void {
        value = next;
      },
      getSelection: () => selection,
      applyEdit(edit): void {
        value = value.slice(0, edit.from) + edit.insert + value.slice(edit.to);
        selection = { from: edit.selectionFrom, to: edit.selectionTo };
        notify();
      },
      focus: () => {},
      onChange(listener: () => void): void {
        listeners.push(listener);
      },
      destroy(): void {
        destroyed = true;
      },
      type(next: string): void {
        value = next;
        selection = { from: next.length, to: next.length };
        notify();
      },
      select(from: number, to: number): void {
        selection = { from, to };
      },
      destroyed: () => destroyed,
    };
    return built;
  };

  return { factory, surface: () => built };
};
