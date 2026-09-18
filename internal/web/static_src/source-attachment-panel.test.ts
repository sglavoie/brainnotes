// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from "vitest";

import { readAttachmentElements } from "./source-editor-elements";
import { initAttachmentPanel, renderAttachmentList } from "./source-attachment-panel";
import type { AttachmentEntry, AttachmentTracker, UploadFile } from "./source-attachments";

// Choosing, pasting and dropping are three doors onto one path, and the point of
// testing all three is that they stay one: a screenshot pasted from the clipboard
// and the same file dragged from a folder must produce the same row. The panel
// decides nothing — every test below asserts a call on the tracker, never an
// outcome.

const markup = `
<section data-source-editor>
  <div data-source-surface></div>
  <input type="file" data-source-attachment-input multiple hidden>
  <button type="button" data-source-attachment-choose>Attach files…</button>
  <section data-source-attachments hidden>
    <ul data-source-attachment-list></ul>
  </section>
</section>
`;

type StubTracker = AttachmentTracker & {
  added: UploadFile[][];
  retried: string[];
  canceled: string[];
};

const stubTracker = (entries: AttachmentEntry[] = []): StubTracker => {
  const added: UploadFile[][] = [];
  const retried: string[] = [];
  const canceled: string[] = [];
  return {
    added,
    retried,
    canceled,
    entries: () => entries,
    add: (files) => added.push([...files]),
    retry: (id) => retried.push(id),
    cancel: (id) => canceled.push(id),
    pendingIDs: () => [],
    settle: () => {},
    uploading: () => false,
    dispose: () => {},
  };
};

const entry = (over: Partial<AttachmentEntry> = {}): AttachmentEntry => ({
  id: "row-1",
  filename: "photo.png",
  size: 10,
  state: "uploading",
  loaded: 4,
  message: "",
  uploadID: "",
  relPath: "",
  ...over,
});

const file = (name: string): UploadFile => new File(["bytes"], name) as unknown as UploadFile;

// jsdom has no useful DataTransfer, and a real one cannot be built in a test
// anyway, so the three file-carrying events are assembled by hand. What each
// handler reads is a short list — `files` and `types` — and this supplies it.
const fileEvent = (type: string, key: "clipboardData" | "dataTransfer", files: UploadFile[]) => {
  const event = new Event(type, { bubbles: true, cancelable: true });
  Object.defineProperty(event, key, {
    value: { files, types: files.length > 0 ? ["Files"] : ["text/plain"] },
  });
  return event;
};

let panel: HTMLElement;
let elements: ReturnType<typeof readAttachmentElements>;

beforeEach(() => {
  document.body.innerHTML = markup;
  panel = document.querySelector("[data-source-editor]") as HTMLElement;
  elements = readAttachmentElements(panel);
});

describe("readAttachmentElements", () => {
  // The drop target is the editing surface, not the attachment list: a file is
  // dropped where the text is, and the list is hidden until something is in it.
  it("takes the drop zone from the editing surface", () => {
    expect(elements.dropZone).toBe(panel.querySelector("[data-source-surface]"));
    expect(elements.input).toBeInstanceOf(HTMLInputElement);
    expect(elements.choose).toBeInstanceOf(HTMLButtonElement);
  });
});

describe("initAttachmentPanel", () => {
  it("opens the file chooser from the button, since the input itself is hidden", () => {
    const tracker = stubTracker();
    initAttachmentPanel({ elements, tracker, doc: document });
    const click = vi.spyOn(elements.input as HTMLInputElement, "click").mockImplementation(() => {});
    elements.choose?.click();
    expect(click).toHaveBeenCalledOnce();
  });

  it("hands chosen files over and clears the input so the same file can be chosen twice", () => {
    const tracker = stubTracker();
    initAttachmentPanel({ elements, tracker, doc: document });
    const input = elements.input as HTMLInputElement;
    Object.defineProperty(input, "files", { value: [file("a.png")], configurable: true });
    input.dispatchEvent(new Event("change"));

    expect(tracker.added).toHaveLength(1);
    expect(tracker.added[0][0].name).toBe("a.png");
    expect(input.value).toBe("");
  });

  it("takes a pasted file and keeps the browser from pasting it as well", () => {
    const tracker = stubTracker();
    initAttachmentPanel({ elements, tracker, doc: document });
    const event = fileEvent("paste", "clipboardData", [file("shot.png")]);
    elements.dropZone?.dispatchEvent(event);

    expect(tracker.added[0][0].name).toBe("shot.png");
    expect(event.defaultPrevented).toBe(true);
  });

  // Intercepting a paste that carries no file would put this module between a
  // person and every ordinary copy of a paragraph.
  it("leaves a paste that carries no file entirely alone", () => {
    const tracker = stubTracker();
    initAttachmentPanel({ elements, tracker, doc: document });
    const event = fileEvent("paste", "clipboardData", []);
    elements.dropZone?.dispatchEvent(event);

    expect(tracker.added).toEqual([]);
    expect(event.defaultPrevented).toBe(false);
  });

  // Without preventDefault on dragover the drop event never fires at all.
  it("accepts a drag carrying files and marks the surface while it is over it", () => {
    const tracker = stubTracker();
    initAttachmentPanel({ elements, tracker, doc: document });
    const over = fileEvent("dragover", "dataTransfer", [file("a.png")]);
    elements.dropZone?.dispatchEvent(over);
    expect(over.defaultPrevented).toBe(true);
    expect(elements.dropZone?.classList.contains("is-drop-target")).toBe(true);

    elements.dropZone?.dispatchEvent(new Event("dragleave", { bubbles: true }));
    expect(elements.dropZone?.classList.contains("is-drop-target")).toBe(false);
  });

  it("ignores a drag that carries no files, so a dragged selection still behaves", () => {
    const tracker = stubTracker();
    initAttachmentPanel({ elements, tracker, doc: document });
    const over = fileEvent("dragover", "dataTransfer", []);
    elements.dropZone?.dispatchEvent(over);
    expect(over.defaultPrevented).toBe(false);
  });

  // Without preventDefault on drop the browser navigates away from the editor to
  // display the file, taking every unsaved word with it; without stopPropagation
  // CodeMirror's own handler inserts the file's name as text beside the link.
  it("takes a dropped file, and stops both the browser and the editor from reacting", () => {
    const tracker = stubTracker();
    initAttachmentPanel({ elements, tracker, doc: document });
    const event = fileEvent("drop", "dataTransfer", [file("dropped.png")]);
    const stop = vi.fn();
    Object.defineProperty(event, "stopPropagation", { value: stop });
    elements.dropZone?.dispatchEvent(event);

    expect(tracker.added[0][0].name).toBe("dropped.png");
    expect(event.defaultPrevented).toBe(true);
    expect(stop).toHaveBeenCalledOnce();
    expect(elements.dropZone?.classList.contains("is-drop-target")).toBe(false);
  });

  it("routes the row buttons through one delegated listener", () => {
    const tracker = stubTracker([entry({ id: "row-9", state: "failed", message: "nope" })]);
    initAttachmentPanel({ elements, tracker, doc: document });

    const list = elements.list as HTMLElement;
    list.querySelector<HTMLButtonElement>("[data-source-attachment-retry]")?.click();
    list.querySelector<HTMLButtonElement>("[data-source-attachment-cancel]")?.click();
    expect(tracker.retried).toEqual(["row-9"]);
    expect(tracker.canceled).toEqual(["row-9"]);
  });

  it("stops listening when its unbind is called", () => {
    const tracker = stubTracker();
    const unbind = initAttachmentPanel({ elements, tracker, doc: document });
    unbind();
    elements.dropZone?.dispatchEvent(fileEvent("paste", "clipboardData", [file("a.png")]));
    expect(tracker.added).toEqual([]);
  });
});

describe("renderAttachmentList", () => {
  it("hides the whole section when nothing is staged", () => {
    renderAttachmentList(elements.list, [], document);
    expect((elements.panel as HTMLElement).hidden).toBe(true);
    expect(elements.list?.children).toHaveLength(0);
  });

  it("shows the section as soon as there is a row, and hides it again when the last goes", () => {
    renderAttachmentList(elements.list, [entry()], document);
    expect((elements.panel as HTMLElement).hidden).toBe(false);
    renderAttachmentList(elements.list, [], document);
    expect((elements.panel as HTMLElement).hidden).toBe(true);
  });

  it("gives an upload in flight a progress bar and a way to stop it", () => {
    renderAttachmentList(elements.list, [entry({ size: 10, loaded: 4 })], document);
    const row = elements.list?.firstElementChild as HTMLElement;
    expect(row.getAttribute("data-source-attachment-state")).toBe("uploading");
    expect(row.textContent).toContain("40%");

    const progress = row.querySelector("progress") as HTMLProgressElement;
    expect(progress.max).toBe(10);
    expect(progress.value).toBe(4);
    expect(row.querySelector("[data-source-attachment-retry]")).toBeNull();
    expect(row.querySelector("[data-source-attachment-cancel]")?.textContent).toBe("Cancel");
  });

  // "Staged" is the word that has to be read correctly: the bytes are on the
  // server and are not in the vault, and a person who reads "uploaded" there and
  // closes the tab has lost the file.
  it("names a staged row by its destination and offers to remove it", () => {
    renderAttachmentList(
      elements.list,
      [entry({ state: "staged", uploadID: "u-1", relPath: "attachments/photo-1.png" })],
      document
    );
    const row = elements.list?.firstElementChild as HTMLElement;
    expect(row.textContent).toContain("attachments/photo-1.png");
    expect(row.textContent).toContain("Staged");
    expect(row.querySelector("[data-source-attachment-cancel]")?.textContent).toBe("Remove");
    expect(row.querySelector("progress")).toBeNull();
  });

  it("offers a retry on a failure and says why it failed", () => {
    renderAttachmentList(
      elements.list,
      [entry({ state: "failed", message: "The vault is busy." })],
      document
    );
    const row = elements.list?.firstElementChild as HTMLElement;
    expect(row.textContent).toContain("The vault is busy.");
    expect(row.querySelector("[data-source-attachment-retry]")).not.toBeNull();
  });

  it("does nothing at all on a page with no list", () => {
    expect(() => renderAttachmentList(null, [entry()], document)).not.toThrow();
  });
});
