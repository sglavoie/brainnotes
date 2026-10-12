// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import type { SourceClient } from "./source-save";
import {
  cleanup,
  mount,
  saveFailure,
  saveSuccess,
  stubClaims,
  committed,
} from "./source-editor-controller.test-helpers";

afterEach(cleanup);

describe("attachment claims on save", () => {
  it("sends the owner and the ids this page is holding", async () => {
    const claims = stubClaims(["u-1", "u-2"]);
    const harness = mount({}, 0, claims);
    await harness.controller.load();
    harness.surface().type("edited\n");

    await harness.controller.save();
    expect(harness.client.save.mock.calls[0][0]).toMatchObject({
      owner: "tab-7",
      attachments: ["u-1", "u-2"],
    });
  });

  it("settles what the vault answered, not what the request asked for", async () => {
    const claims = stubClaims(["u-1", "u-2"]);
    const harness = mount(
      {
        save: vi
          .fn()
          .mockResolvedValue(saveSuccess({ attachments: committed })) as unknown as SourceClient["save"],
      },
      0,
      claims
    );
    await harness.controller.load();
    harness.surface().type("edited\n");

    await harness.controller.save();
    expect(claims.settled).toEqual([committed]);
  });

});

// The three answers that are not a plain success. What a save committed is the
// server's statement, so each of these is about believing that statement rather
// than the request that provoked it.
describe("attachment claims a save did not plainly commit", () => {
  // The bytes are on disk; only the content history missed them. A page that
  // went on owing those ids would cancel them on the way out and delete
  // attachments the note already links.
  it("settles on the snapshot failure too, because that arm committed them as well", async () => {
    const claims = stubClaims(["u-1"]);
    const harness = mount(
      {
        save: vi.fn().mockResolvedValue(
          saveFailure({
            ok: false,
            code: "snapshot_failed",
            message: "the change is on disk and further writes are blocked",
            revision: "r7",
            changed: true,
            attachments: committed,
          })
        ) as unknown as SourceClient["save"],
      },
      0,
      claims
    );
    await harness.controller.load();
    harness.surface().type("edited\n");

    await harness.controller.save();
    expect(claims.settled).toEqual([committed]);
  });

  // A conflict wrote nothing, so the ids are still this page's to send with the
  // merge that follows.
  it("settles nothing a refusal did not commit", async () => {
    const claims = stubClaims(["u-1"]);
    const harness = mount(
      {
        save: vi.fn().mockResolvedValue(
          saveFailure({
            ok: false,
            code: "revision_conflict",
            message: "The note changed since it was read.",
            content: "theirs\n",
            revision: "r9",
          })
        ) as unknown as SourceClient["save"],
      },
      0,
      claims
    );
    await harness.controller.load();
    harness.surface().type("edited\n");

    await harness.controller.save();
    expect(claims.settled).toEqual([[]]);
  });

  // The request never produced an answer, so what the vault holds is unknown.
  // Settling here would drop an id whose file may never have been committed.
  it("settles nothing when the request never came back", async () => {
    const claims = stubClaims(["u-1"]);
    const harness = mount(
      { save: vi.fn().mockRejectedValue(new Error("offline")) as unknown as SourceClient["save"] },
      0,
      claims
    );
    await harness.controller.load();
    harness.surface().type("edited\n");

    await harness.controller.save();
    expect(claims.settled).toEqual([]);
  });
});
