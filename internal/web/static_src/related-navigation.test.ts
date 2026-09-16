// @vitest-environment jsdom
//
// The Related panel's Open against a server that does not start editors. These
// live apart from related-workflow.test.ts because they stub `window` and are
// about one decision — destination versus warning — rather than the panel's
// tab/feedback/focus workflow.

import { afterEach, describe, expect, it, vi } from "vitest";

import { initRelatedNotes } from "./related";
import {
  itemPayload,
  jsonResponse,
  payload,
  postFields,
  requestOptions,
  rootHTML,
  waitForExpect,
} from "./related.test-helpers";

afterEach(() => {
  document.body.innerHTML = "";
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const openResponder = (overrides: Record<string, unknown>) =>
  vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    if (init?.method === "POST") {
      return jsonResponse(
        itemPayload("open_editor", postFields(init).get("candidate") || "", {
          opened: true,
          ...overrides,
        })
      );
    }
    const { mode, limit } = requestOptions(input);
    return jsonResponse(payload(mode, limit));
  });

const clickOpen = async (fetchMock: ReturnType<typeof openResponder>): Promise<void> => {
  initRelatedNotes({ fetch: fetchMock });
  await waitForExpect(() =>
    expect(document.querySelector('[data-related-card="balanced-1"]')).not.toBeNull()
  );
  document
    .querySelector<HTMLButtonElement>(
      '[data-related-card="balanced-1"] [data-related-action="open_editor"]'
    )
    ?.click();
};

const statusText = (): string => document.querySelector("[data-related-status]")?.textContent || "";

describe("related notes open navigation", () => {
  // A hosted or read-only server answers Open with a route instead of starting
  // an editor. The panel has no wording for "opened" in that case, so it has to
  // navigate — and it must navigate only to a note route, because the value
  // comes off the wire.
  it("navigates when the server answers Open with a destination", async () => {
    document.body.innerHTML = rootHTML();
    const assign = vi.fn();
    vi.stubGlobal("window", { ...window, location: { assign } });

    await clickOpen(openResponder({ recorded: true, destination: "/n/Folder/balanced-1" }));

    await waitForExpect(() => expect(assign).toHaveBeenCalledWith("/n/Folder/balanced-1"));
    expect(statusText()).not.toContain("Opened");
  });

  it("ignores an Open destination that is not a note route", async () => {
    document.body.innerHTML = rootHTML();
    const assign = vi.fn();
    vi.stubGlobal("window", { ...window, location: { assign } });

    await clickOpen(
      openResponder({ recorded: true, destination: "https://evil.example/n/Folder/balanced-1" })
    );

    await waitForExpect(() => expect(statusText()).toContain("recorded"));
    expect(assign).not.toHaveBeenCalled();
  });

  // The server fills in the destination before it tries to record the open, so
  // a failed recall write arrives as a destination *and* a warning. Navigating
  // would replace the page holding the only report of it.
  it("reports a failed recall write instead of navigating away from it", async () => {
    document.body.innerHTML = rootHTML();
    const assign = vi.fn();
    vi.stubGlobal("window", { ...window, location: { assign } });

    await clickOpen(
      openResponder({
        recorded: false,
        destination: "/n/Folder/balanced-1",
        warning: "Recall history could not be recorded.",
      })
    );

    await waitForExpect(() => expect(statusText()).toContain("Recall history could not be recorded."));
    expect(statusText()).toContain("title link");
    expect(statusText()).not.toContain("Editor opened");
    expect(assign).not.toHaveBeenCalled();
  });

  it("names the editor when a local server started one and could not record it", async () => {
    document.body.innerHTML = rootHTML();

    await clickOpen(
      openResponder({ recorded: false, warning: "Recall history could not be recorded." })
    );

    await waitForExpect(() => expect(statusText()).toContain("Editor opened."));
    expect(statusText()).toContain("Recall history could not be recorded.");
  });
});
