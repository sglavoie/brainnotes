// @vitest-environment jsdom

import { describe, expect, it, vi } from "vitest";

import {
  createAttachmentTracker,
  type AttachmentHost,
  type AttachmentTracker,
  type UploadFile,
} from "./source-attachments";
import type { UploadRequestLike } from "./source-attachment-upload";

// The tracker is what the editor owes the vault: every staged upload is a file
// on the server that no note mentions until a save commits it. These tests are
// about that debt — that it is taken on when staging succeeds, handed over when a
// save answers, and given back when somebody cancels — and none of them touch
// the DOM, because the tracker does not.

type FakeRequest = UploadRequestLike & {
  url: string;
  aborts: number;
  answer(status: number, payload: unknown): void;
  die(): void;
  report(loaded: number): void;
};

const fakeRequest = (): FakeRequest => {
  const request: FakeRequest = {
    url: "",
    aborts: 0,
    upload: { onprogress: null },
    onload: null,
    onerror: null,
    onabort: null,
    status: 0,
    responseText: "",
    open: (_method, url) => {
      request.url = url;
    },
    setRequestHeader: () => {},
    send: () => {},
    abort: () => {
      request.aborts += 1;
      request.onabort?.();
    },
    answer: (status, payload) => {
      request.status = status;
      request.responseText = JSON.stringify(payload);
      request.onload?.();
    },
    die: () => request.onerror?.(),
    report: (loaded) =>
      request.upload.onprogress?.({ lengthComputable: true, loaded, total: 5 }),
  };
  return request;
};

const file = (name: string, bytes = "bytes"): UploadFile =>
  new File([bytes], name) as unknown as UploadFile;

type Harness = {
  tracker: AttachmentTracker;
  host: { links: string[]; statuses: { message: string; kind: string }[]; changes: number };
  requests: FakeRequest[];
  cancels: { url: string; body: URLSearchParams }[];
};

const harness = (maxBytes = 0): Harness => {
  const links: string[] = [];
  const statuses: { message: string; kind: string }[] = [];
  const requests: FakeRequest[] = [];
  const cancels: { url: string; body: URLSearchParams }[] = [];
  let changes = 0;

  const host: AttachmentHost = {
    insertLink: (markdown) => links.push(markdown),
    setStatus: (message, kind) => statuses.push({ message, kind }),
    changed: () => {
      changes += 1;
    },
  };

  let ids = 0;
  const tracker = createAttachmentTracker({
    owner: "tab-1",
    token: "csrf-token",
    uploadEndpoint: "/api/attachments/upload",
    cancelEndpoint: "/api/attachments/cancel",
    maxBytes,
    host,
    newID: () => `row-${++ids}`,
    newRequest: () => {
      const request = fakeRequest();
      requests.push(request);
      return request;
    },
    fetch: (async (url: string, init: RequestInit) => {
      cancels.push({ url, body: new URLSearchParams(init.body as string) });
      return new Response("{}", { status: 200 });
    }) as unknown as typeof fetch,
  });

  return {
    tracker,
    requests,
    cancels,
    host: {
      links,
      statuses,
      get changes(): number {
        return changes;
      },
    },
  };
};

const staged = (uploadID: string, relPath: string, filename: string): unknown => ({
  ok: true,
  upload_id: uploadID,
  rel_path: relPath,
  filename,
  size: 5,
  sha256: "abc",
});

describe("createAttachmentTracker", () => {
  it("opens a row per file and sends each one to the owner's staging area", () => {
    const h = harness();
    h.tracker.add([file("a.png"), file("b.pdf")]);
    expect(h.tracker.entries().map((entry) => entry.filename)).toEqual(["a.png", "b.pdf"]);
    expect(h.tracker.entries().every((entry) => entry.state === "uploading")).toBe(true);
    expect(h.requests.map((request) => request.url)).toEqual([
      "/api/attachments/upload?owner=tab-1&name=a.png",
      "/api/attachments/upload?owner=tab-1&name=b.pdf",
    ]);
    expect(h.tracker.uploading()).toBe(true);
  });

  it("carries progress onto the row and asks the panel to redraw", () => {
    const h = harness();
    h.tracker.add([file("a.png")]);
    const before = h.host.changes;
    h.requests[0].report(3);
    expect(h.tracker.entries()[0].loaded).toBe(3);
    expect(h.host.changes).toBeGreaterThan(before);
  });

  // The link goes in when the upload is *staged*, not when the save commits it:
  // staging reserves the destination, and a person watching a file upload expects
  // the reference to appear with it.
  it("inserts the link and owes the id as soon as the server stages it", async () => {
    const h = harness();
    h.tracker.add([file("photo.png")]);
    h.requests[0].answer(200, staged("u-1", "attachments/photo-1.png", "photo-1.png"));
    await vi.waitFor(() => expect(h.tracker.entries()[0].state).toBe("staged"));

    expect(h.host.links).toEqual(["![photo-1.png](attachments/photo-1.png)"]);
    expect(h.tracker.pendingIDs()).toEqual(["u-1"]);
    expect(h.tracker.entries()[0]).toMatchObject({
      filename: "photo-1.png",
      relPath: "attachments/photo-1.png",
      uploadID: "u-1",
      loaded: h.tracker.entries()[0].size,
    });
    expect(h.tracker.uploading()).toBe(false);
    expect(h.host.statuses.at(-1)?.kind).toBe("is-success");
  });

  it("reports a refusal on the row and owes nothing for it", async () => {
    const h = harness();
    h.tracker.add([file("photo.png")]);
    h.requests[0].answer(500, { ok: false, message: "The vault is busy." });
    await vi.waitFor(() => expect(h.tracker.entries()[0].state).toBe("failed"));

    expect(h.tracker.entries()[0].message).toBe("The vault is busy.");
    expect(h.tracker.pendingIDs()).toEqual([]);
    expect(h.host.links).toEqual([]);
    expect(h.host.statuses.at(-1)?.kind).toBe("is-error");
  });

  it("retries a failed row through the same slot rather than opening a second one", async () => {
    const h = harness();
    h.tracker.add([file("photo.png")]);
    h.requests[0].die();
    await vi.waitFor(() => expect(h.tracker.entries()[0].state).toBe("failed"));

    const id = h.tracker.entries()[0].id;
    h.tracker.retry(id);
    expect(h.tracker.entries()).toHaveLength(1);
    expect(h.tracker.entries()[0].state).toBe("uploading");
    expect(h.tracker.entries()[0].message).toBe("");
    expect(h.requests).toHaveLength(2);

    h.requests[1].answer(200, staged("u-2", "attachments/photo.png", "photo.png"));
    await vi.waitFor(() => expect(h.tracker.pendingIDs()).toEqual(["u-2"]));
  });

  // Retrying a staged upload would stage a second copy of the same bytes, and the
  // save would commit both — one of them linked by nothing.
  it("refuses to retry an upload the server already holds", async () => {
    const h = harness();
    h.tracker.add([file("photo.png")]);
    h.requests[0].answer(200, staged("u-1", "attachments/photo.png", "photo.png"));
    await vi.waitFor(() => expect(h.tracker.entries()[0].state).toBe("staged"));

    h.tracker.retry(h.tracker.entries()[0].id);
    expect(h.requests).toHaveLength(1);
  });

});

// The other half of the tracker's job: letting go. A row is dropped by a person
// cancelling it, by a save committing it, or by the page going away, and the
// three differ only in what the server is told.
describe("createAttachmentTracker: letting go of a row", () => {
  it("aborts an upload in flight and tells nobody, because nothing was staged", () => {
    const h = harness();
    h.tracker.add([file("photo.png")]);
    h.tracker.cancel(h.tracker.entries()[0].id);
    expect(h.requests[0].aborts).toBe(1);
    expect(h.tracker.entries()).toEqual([]);
    expect(h.cancels).toEqual([]);
    expect(h.host.statuses.at(-1)?.message).toBe("photo.png was not uploaded.");
  });

  it("asks the server to drop bytes it is holding when a staged row is removed", async () => {
    const h = harness();
    h.tracker.add([file("photo.png")]);
    h.requests[0].answer(200, staged("u-1", "attachments/photo.png", "photo.png"));
    await vi.waitFor(() => expect(h.tracker.entries()[0].state).toBe("staged"));

    h.tracker.cancel(h.tracker.entries()[0].id);
    expect(h.tracker.entries()).toEqual([]);
    expect(h.tracker.pendingIDs()).toEqual([]);
    await vi.waitFor(() => expect(h.cancels).toHaveLength(1));
    expect(h.cancels[0].url).toBe("/api/attachments/cancel");
    expect(h.cancels[0].body.get("upload_id")).toBe("u-1");
    expect(h.cancels[0].body.get("owner")).toBe("tab-1");
    expect(h.host.statuses.at(-1)?.message).toContain("It is not in the vault.");
  });

  it("ignores a cancel for a row that is already gone", () => {
    const h = harness();
    expect(() => h.tracker.cancel("row-404")).not.toThrow();
  });

  // The vault's limit is configurable downward from the package ceiling, so the
  // browser refuses at the number the page published rather than a constant.
  it("refuses an oversize file without sending a byte, and keeps the row", () => {
    const h = harness(4);
    h.tracker.add([file("big.png", "123456")]);
    expect(h.requests).toEqual([]);
    expect(h.tracker.entries()[0].state).toBe("failed");
    expect(h.tracker.entries()[0].message).toContain("4-byte limit");
    expect(h.tracker.pendingIDs()).toEqual([]);
  });

  describe("settle", () => {
    const stage = async (h: Harness, uploadID: string, relPath: string): Promise<void> => {
      const before = h.requests.length;
      h.tracker.add([file(relPath.slice(relPath.lastIndexOf("/") + 1))]);
      h.requests[before].answer(200, staged(uploadID, relPath, "photo.png"));
      await vi.waitFor(() => expect(h.tracker.pendingIDs()).toContain(uploadID));
    };

    it("drops the rows the save committed and keeps the ones it did not", async () => {
      const h = harness();
      await stage(h, "u-1", "attachments/one.png");
      await stage(h, "u-2", "attachments/two.png");

      h.tracker.settle([{ uploadID: "u-1", relPath: "attachments/one.png" }]);
      expect(h.tracker.pendingIDs()).toEqual(["u-2"]);
      expect(h.tracker.entries()).toHaveLength(1);
    });

    it("ignores an id this page never staged", async () => {
      const h = harness();
      await stage(h, "u-1", "attachments/one.png");
      h.tracker.settle([{ uploadID: "someone-else", relPath: "attachments/x.png" }]);
      expect(h.tracker.pendingIDs()).toEqual(["u-1"]);
    });

    // Staging reserved the destination, so a save filing it elsewhere means the
    // link already in the note names a file that is not there. Saying so is the
    // only remedy: rewriting the person's text under them would be a second
    // guess on top of a broken promise.
    it("says so, loudly, when the vault filed the file somewhere else", async () => {
      const h = harness();
      await stage(h, "u-1", "attachments/one.png");
      h.tracker.settle([{ uploadID: "u-1", relPath: "attachments/one-2.png" }]);

      const last = h.host.statuses.at(-1);
      expect(last?.kind).toBe("is-error");
      expect(last?.message).toContain("attachments/one-2.png");
      expect(last?.message).toContain("attachments/one.png");
      expect(h.tracker.entries()).toEqual([]);
    });
  });

  // The page is going away: there is nobody to tell and no request that would
  // survive long enough to arrive, so what is staged is left for the sweep.
  it("aborts everything in flight on dispose and sends no cancels", () => {
    const h = harness();
    h.tracker.add([file("a.png"), file("b.png")]);
    h.tracker.dispose();
    expect(h.requests.map((request) => request.aborts)).toEqual([1, 1]);
    expect(h.cancels).toEqual([]);
  });
});
