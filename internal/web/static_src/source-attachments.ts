import type { StatusKind } from "./status";
import { attachmentLinkText } from "./source-attachment-link";
import {
  cancelStagedUpload,
  uploadAttachment,
  type StagedUpload,
  type UploadHandle,
  type UploadRequestLike,
} from "./source-attachment-upload";

// What this page has staged, and what a save must carry.
//
// The tracker is the editor's half of the two-step the server describes in
// internal/web/attachments_api.go: bytes go into private staging first and enter
// the vault only when a save commits them. So an upload id is *owed* to the note
// from the moment it is staged until either a save commits it or a cancel drops
// it, and the one thing this module must never do is lose one silently — a
// staged file nobody commits is disk the sweep collects, and a committed file
// nobody links is a file in the vault no note mentions.
//
// It holds no DOM. The panel renders `entries()` and the controller supplies
// `insertLink`, which is what lets every state transition here be asserted under
// jsdom with no elements at all.

export type AttachmentState = "uploading" | "staged" | "failed" | "canceled";

export type AttachmentEntry = {
  // id names the row, not the upload. It exists before there is an upload id
  // and survives a retry, so the panel's buttons keep pointing at the same file.
  id: string;
  filename: string;
  size: number;
  state: AttachmentState;
  loaded: number;
  message: string;
  // uploadID and relPath are empty until the server has staged the bytes.
  uploadID: string;
  relPath: string;
};

// UploadFile is a browser File without naming the class: the same three fields
// come off a drop, a paste and a file input, and taking the structural type is
// what lets a test hand over a Blob with a name.
export type UploadFile = Blob & { readonly name: string };

export type AttachmentHost = {
  // insertLink puts the reference in the note at the cursor. It is called once,
  // when the upload is staged — not when the save commits it — because the
  // destination is reserved at staging time and a person watching a file upload
  // expects the link to appear with it.
  insertLink(markdown: string): void;
  setStatus(message: string, kind: StatusKind): void;
  // changed asks the panel to re-render. Every mutation below calls it, so the
  // panel never has to poll or subscribe to individual entries.
  changed(): void;
};

export type AttachmentTrackerOptions = {
  owner: string;
  token: string;
  uploadEndpoint: string;
  cancelEndpoint: string;
  maxBytes: number;
  host: AttachmentHost;
  newRequest?: () => UploadRequestLike;
  fetch?: typeof fetch;
  newID: () => string;
};

export type AttachmentTracker = {
  entries(): AttachmentEntry[];
  add(files: readonly UploadFile[]): void;
  retry(id: string): void;
  cancel(id: string): void;
  // pendingIDs is exactly what the next save carries: every upload the server
  // has staged and no save has committed, oldest first, which is the order the
  // links went into the note.
  pendingIDs(): string[];
  // settle drops the entries a save committed. It is given what the *server*
  // answered rather than what the request carried, because a replayed save
  // answers from an earlier call's record and an id it does not name has not
  // been committed by anything.
  settle(committed: readonly { uploadID: string; relPath: string }[]): void;
  uploading(): boolean;
  dispose(): void;
};

type TrackedUpload = AttachmentEntry & {
  file: UploadFile;
  handle: UploadHandle | null;
};

export const createAttachmentTracker = (options: AttachmentTrackerOptions): AttachmentTracker => {
  const tracked: TrackedUpload[] = [];
  const { host } = options;

  const find = (id: string): TrackedUpload | undefined => tracked.find((item) => item.id === id);

  return {
    entries: (): AttachmentEntry[] => tracked.map(publicEntry),

    add: (files): void => addFiles(options, tracked, files),

    retry(id): void {
      const item = find(id);
      // A staged upload is deliberately not retryable. Its bytes are already on
      // the server and its link is already in the note; sending them again would
      // stage a second copy the save would commit beside the first.
      if (item && (item.state === "failed" || item.state === "canceled")) {
        startUpload(options, item);
      }
    },

    cancel(id): void {
      const item = find(id);
      if (item) {
        cancelEntry(options, tracked, item);
      }
    },

    pendingIDs: (): string[] =>
      tracked.filter((item) => item.state === "staged").map((item) => item.uploadID),

    settle: (committed): void => settleCommitted(host, tracked, committed),

    uploading: (): boolean => tracked.some((item) => item.state === "uploading"),

    dispose(): void {
      // Aborted, not cancelled: the page is going away, so there is nobody to
      // tell and no request that would survive long enough to arrive. What is
      // already staged is left for the server's own sweep.
      for (const item of tracked) {
        item.handle?.cancel();
        item.handle = null;
      }
    },
  };
};

// startUpload sends one file and hands its row over to the answer. It is the
// single entry to uploading, so a first attempt and a retry are the same three
// resets and the same request.
const startUpload = (options: AttachmentTrackerOptions, item: TrackedUpload): void => {
  const { host } = options;
  item.state = "uploading";
  item.loaded = 0;
  item.message = "";
  item.handle = uploadAttachment({
    endpoint: options.uploadEndpoint,
    owner: options.owner,
    token: options.token,
    filename: item.file.name,
    file: item.file,
    newRequest: options.newRequest,
    onProgress: (progress) => {
      item.loaded = progress.loaded;
      host.changed();
    },
  });
  void item.handle.outcome.then((outcome) => {
    item.handle = null;
    applyUploadOutcome(item, outcome, host);
    host.changed();
  });
  host.changed();
};

const addFiles = (
  options: AttachmentTrackerOptions,
  tracked: TrackedUpload[],
  files: readonly UploadFile[]
): void => {
  for (const file of files) {
    const refusal = oversizeMessage(file, options.maxBytes);
    const item: TrackedUpload = {
      id: options.newID(),
      filename: file.name,
      size: file.size,
      state: refusal === "" ? "uploading" : "failed",
      loaded: 0,
      message: refusal,
      uploadID: "",
      relPath: "",
      file,
      handle: null,
    };
    tracked.push(item);
    if (refusal === "") {
      startUpload(options, item);
    } else {
      // A refused file still gets a row. It is the only place the refusal is
      // said beside the file it is about, and a silent drop would look exactly
      // like a file the browser never noticed.
      options.host.changed();
    }
  }
};

const cancelEntry = (
  options: AttachmentTrackerOptions,
  tracked: TrackedUpload[],
  item: TrackedUpload
): void => {
  item.handle?.cancel();
  if (item.uploadID !== "") {
    void cancelStagedUpload({
      endpoint: options.cancelEndpoint,
      owner: options.owner,
      uploadID: item.uploadID,
      token: options.token,
      fetch: options.fetch,
    });
  }
  // The row is dropped rather than marked, and the link the insert put in the
  // note is deliberately left alone: the text belongs to the person now, and
  // reaching into their document to delete a line they may have edited is a
  // worse outcome than a reference they can remove themselves.
  remove(tracked, item);
  options.host.setStatus(cancelMessage(item), "");
  options.host.changed();
};

const settleCommitted = (
  host: AttachmentHost,
  tracked: TrackedUpload[],
  committed: readonly { uploadID: string; relPath: string }[]
): void => {
  for (const saved of committed) {
    const item = tracked.find((candidate) => candidate.uploadID === saved.uploadID);
    if (!item) {
      continue;
    }
    if (saved.relPath !== "" && saved.relPath !== item.relPath) {
      // Staging reserves the destination, so the two agreeing is the contract
      // rather than luck. When it is broken the link in the note names a file
      // that is not there, and saying so is the only remedy this page has:
      // rewriting the person's text under them would be a second guess on top
      // of a broken promise.
      host.setStatus(
        `${item.filename} was filed as ${saved.relPath}, not ${item.relPath}. Update the link in the note.`,
        "is-error"
      );
    }
    remove(tracked, item);
  }
  host.changed();
};

const applyUploadOutcome = (
  item: TrackedUpload,
  outcome: { kind: string; staged?: StagedUpload; message?: string },
  host: AttachmentHost
): void => {
  if (outcome.kind === "staged" && outcome.staged) {
    item.state = "staged";
    item.uploadID = outcome.staged.uploadID;
    item.relPath = outcome.staged.relPath;
    item.filename = outcome.staged.filename || item.filename;
    item.loaded = item.size;
    item.message = "";
    host.insertLink(attachmentLinkText(outcome.staged.relPath));
    host.setStatus(`${item.filename} is staged. It enters the vault when you save.`, "is-success");
    return;
  }
  if (outcome.kind === "canceled") {
    item.state = "canceled";
    item.message = "Upload stopped.";
    return;
  }
  item.state = "failed";
  item.message = outcome.message || "The upload failed.";
  host.setStatus(`${item.filename}: ${item.message}`, "is-error");
};

const remove = (tracked: TrackedUpload[], item: TrackedUpload): void => {
  const index = tracked.indexOf(item);
  if (index >= 0) {
    tracked.splice(index, 1);
  }
};

const cancelMessage = (item: TrackedUpload): string =>
  item.state === "staged"
    ? `${item.filename} was dropped from staging. It is not in the vault.`
    : `${item.filename} was not uploaded.`;

// oversizeMessage refuses a file the server would refuse anyway, before it goes
// over the wire. The limit is the vault's own, published on the page, because
// [attachments].max_file_bytes is configurable downward from the ceiling.
const oversizeMessage = (file: UploadFile, maxBytes: number): string =>
  maxBytes > 0 && file.size > maxBytes
    ? `${file.name} is ${file.size} bytes, over this vault's ${maxBytes}-byte limit for one attachment. Nothing was sent.`
    : "";

const publicEntry = (item: TrackedUpload): AttachmentEntry => ({
  id: item.id,
  filename: item.filename,
  size: item.size,
  state: item.state,
  loaded: item.loaded,
  message: item.message,
  uploadID: item.uploadID,
  relPath: item.relPath,
});
