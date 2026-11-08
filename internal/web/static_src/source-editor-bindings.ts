import type { EditorState, SaveContext, SourceEditorController } from "./source-editor-controller";
import type { SourceEditorElements } from "./source-editor-elements";
import type { SourceFormatCommand } from "./source-format";
import { wireConflict } from "./source-editor-conflict";

// Everything that points at the finished controller, which is why it runs last
// and in one place: the key bindings hand back the unbind destroy() owes the
// document, and the pane listener keeps the preview from rendering text nobody
// is looking at.
export const wireEditor = (
  ownerDocument: Document,
  elements: SourceEditorElements,
  state: EditorState,
  controller: SourceEditorController,
  ctx: SaveContext
): (() => void) => {
  wireControls(elements, controller);
  wireConflict(elements, state, controller, ctx);
  ctx.panes.onSelect((pane) => {
    if (pane === "preview") {
      ctx.preview.flush(state.surface?.getValue() ?? "");
    }
  });
  return bindGlobalKeys(ownerDocument, controller);
};

// The save shortcut is bound on the document rather than only in the editor's
// keymap, because the toolbar, the tabs and the conflict buttons are all
// focusable and Ctrl/⌘+S from any of them has to mean the same thing — and it
// has to preventDefault, or the browser offers to save the HTML page instead.
// Capture phase for the same reason: nothing else may claim it first.
const bindGlobalKeys = (
  ownerDocument: Document,
  controller: SourceEditorController
): (() => void) => {
  const onKeyDown = (event: KeyboardEvent): void => {
    // Alt and Shift are excluded rather than ignored: Ctrl/⌘+Alt+S and
    // Ctrl/⌘+Shift+S are bound elsewhere — by browsers, by extensions, by the
    // operating system — and this listener runs in the capture phase, so
    // treating them as Save would swallow a chord that was never meant for the
    // page and silently do something else with it.
    if (
      event.key.toLowerCase() !== "s" ||
      !(event.metaKey || event.ctrlKey) ||
      event.altKey ||
      event.shiftKey
    ) {
      return;
    }
    event.preventDefault();
    void controller.save();
  };

  const onBeforeUnload = (event: BeforeUnloadEvent): void => {
    if (!controller.dirty()) {
      return;
    }
    // The text exists only in this tab. The browser decides what to show; all a
    // page can do is ask for the prompt, and both spellings are needed because
    // browsers disagree about which one arms it.
    event.preventDefault();
    event.returnValue = "";
  };

  ownerDocument.addEventListener("keydown", onKeyDown, true);
  ownerDocument.defaultView?.addEventListener("beforeunload", onBeforeUnload);
  return () => {
    ownerDocument.removeEventListener("keydown", onKeyDown, true);
    ownerDocument.defaultView?.removeEventListener("beforeunload", onBeforeUnload);
  };
};

const wireControls = (
  elements: SourceEditorElements,
  controller: SourceEditorController
): void => {
  for (const button of elements.commands) {
    button.addEventListener("click", () => {
      const command = button.getAttribute("data-source-command") as SourceFormatCommand | null;
      if (command) {
        controller.runCommand(command, { headingLevel: Number(button.getAttribute("data-source-heading-level")) || 1 });
      }
    });
  }
  elements.save?.addEventListener("click", () => void controller.save());
  elements.find?.addEventListener("click", () => controller.openSearch());
};
