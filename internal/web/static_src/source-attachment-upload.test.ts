import { describe, expect, it, vi } from "vitest";

import {
  cancelStagedUpload,
  uploadAttachment,
  uploadURL,
  type UploadOutcome,
  type UploadRequestLike,
} from "./source-attachment-upload";

// The transport is XMLHttpRequest rather than fetch because only the former
// reports upload progress, and the request object is injected so all four ways
// an upload can end are exercised here with no network: answered, refused,
// aborted, and never answered at all.

type FakeRequest = UploadRequestLike & {
  calls: { method: string; url: string }[];
  headers: Record<string, string>;
  body: Blob | null;
  aborts: number;
  answer(status: number, responseText: string): void;
  die(): void;
  report(loaded: number, total: number, lengthComputable?: boolean): void;
};

const fakeRequest = (): FakeRequest => {
  const request: FakeRequest = {
    calls: [],
    headers: {},
    body: null,
    aborts: 0,
    upload: { onprogress: null },
    onload: null,
    onerror: null,
    onabort: null,
    status: 0,
    responseText: "",
    open: (method, url) => {
      request.calls.push({ method, url });
    },
    setRequestHeader: (name, value) => {
      request.headers[name] = value;
    },
    send: (body) => {
      request.body = body;
    },
    abort: () => {
      request.aborts += 1;
      request.onabort?.();
    },
    answer: (status, responseText) => {
      request.status = status;
      request.responseText = responseText;
      request.onload?.();
    },
    die: () => request.onerror?.(),
    report: (loaded, total, lengthComputable = true) =>
      request.upload.onprogress?.({ lengthComputable, loaded, total }),
  };
  return request;
};

const stagedPayload = {
  ok: true,
  upload_id: "u-1",
  filename: "photo-1.png",
  rel_path: "attachments/photo-1.png",
  size: 12,
  sha256: "abc",
};

const start = (
  request: FakeRequest,
  onProgress?: (progress: { loaded: number; total: number }) => void
) =>
  uploadAttachment({
    endpoint: "/api/attachments/upload",
    owner: "tab-1",
    token: "csrf-token",
    filename: "photo.png",
    file: new Blob(["bytes"]),
    newRequest: () => request,
    onProgress,
  });

describe("uploadURL", () => {
  it("carries the owner and the name in the query string, encoded", () => {
    expect(uploadURL("/api/attachments/upload", "tab 1", "a&b.png")).toBe(
      "/api/attachments/upload?owner=tab%201&name=a%26b.png"
    );
  });
});

describe("uploadAttachment", () => {
  // The body is the file's raw bytes, so the CSRF token cannot travel in a form
  // field: the server is streaming the body to disk and never calls ParseForm.
  it("posts the file as the body with the token in the header", () => {
    const request = fakeRequest();
    start(request);
    expect(request.calls).toEqual([
      { method: "POST", url: "/api/attachments/upload?owner=tab-1&name=photo.png" },
    ]);
    expect(request.headers["X-BN-CSRF"]).toBe("csrf-token");
    expect(request.headers.Accept).toBe("application/json");
    expect(request.headers["Content-Type"]).toBe("application/octet-stream");
    expect(request.body).toBeInstanceOf(Blob);
  });

  it("reads the staging envelope, including the name the vault chose", async () => {
    const request = fakeRequest();
    const handle = start(request);
    request.answer(200, JSON.stringify(stagedPayload));
    // Annotated rather than written as a type argument to toEqual: the awaited
    // form of the matcher takes none, and the point is that the whole envelope
    // is accounted for.
    const expected: UploadOutcome = {
      kind: "staged",
      staged: {
        uploadID: "u-1",
        filename: "photo-1.png",
        relPath: "attachments/photo-1.png",
        size: 12,
        sha256: "abc",
      },
    };
    await expect(handle.outcome).resolves.toEqual(expected);
  });

  it("reports the server's own refusal", async () => {
    const request = fakeRequest();
    const handle = start(request);
    request.answer(413, JSON.stringify({ ok: false, message: "That file is too large." }));
    await expect(handle.outcome).resolves.toEqual({
      kind: "failed",
      message: "That file is too large.",
    });
  });

  // A sign-in page served 200 in place of the envelope would otherwise stage an
  // upload id of "" and put an empty link in somebody's note.
  it("refuses a 200 that is not this route's envelope", async () => {
    const request = fakeRequest();
    const handle = start(request);
    request.answer(200, "<html>sign in</html>");
    const outcome = await handle.outcome;
    expect(outcome.kind).toBe("failed");
    expect(outcome.kind === "failed" && outcome.message).toContain("Nothing was uploaded");
  });

  it("refuses an envelope missing the fields the link is built from", async () => {
    const request = fakeRequest();
    const handle = start(request);
    request.answer(200, JSON.stringify({ ok: true, upload_id: "u-1", rel_path: "" }));
    const outcome = await handle.outcome;
    expect(outcome.kind).toBe("failed");
  });

  it("reports a request nobody answered as nothing uploaded", async () => {
    const request = fakeRequest();
    const handle = start(request);
    request.die();
    const outcome = await handle.outcome;
    expect(outcome.kind).toBe("failed");
    expect(outcome.kind === "failed" && outcome.message).toContain("press Retry");
  });

  it("forwards progress only when the length is known", () => {
    const request = fakeRequest();
    const seen: { loaded: number; total: number }[] = [];
    start(request, (progress) => seen.push(progress));
    request.report(0, 0, false);
    request.report(5, 12);
    expect(seen).toEqual([{ loaded: 5, total: 12 }]);
  });

  // An abort is what the person asked for, so it resolves rather than rejects
  // and is not reported as a failure.
  it("resolves a cancel as canceled and aborts exactly once", async () => {
    const request = fakeRequest();
    const handle = start(request);
    handle.cancel();
    handle.cancel();
    await expect(handle.outcome).resolves.toEqual({ kind: "canceled" });
    expect(request.aborts).toBe(1);
  });
});

describe("cancelStagedUpload", () => {
  it("names the owner and the upload in a form, with the token in both places", async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response("{}", { status: 200 }));
    await cancelStagedUpload({
      endpoint: "/api/attachments/cancel",
      owner: "tab-1",
      uploadID: "u-1",
      token: "csrf-token",
      fetch: fetcher as unknown as typeof fetch,
    });
    const [url, init] = fetcher.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/attachments/cancel");
    expect(init.method).toBe("POST");
    expect((init.headers as Record<string, string>)["X-BN-CSRF"]).toBe("csrf-token");
    const body = new URLSearchParams(init.body as string);
    expect(body.get("owner")).toBe("tab-1");
    expect(body.get("upload_id")).toBe("u-1");
    expect(body.get("token")).toBe("csrf-token");
  });

  // A cancel is a courtesy: the server's own sweep collects abandoned staging
  // either way, so a failure here must never surface as a sentence on a page the
  // person has already moved on from.
  it("swallows a failure rather than reporting one", async () => {
    const fetcher = vi.fn().mockRejectedValue(new Error("offline"));
    await expect(
      cancelStagedUpload({
        endpoint: "/api/attachments/cancel",
        owner: "tab-1",
        uploadID: "u-1",
        token: "csrf-token",
        fetch: fetcher as unknown as typeof fetch,
      })
    ).resolves.toBeUndefined();
  });
});
