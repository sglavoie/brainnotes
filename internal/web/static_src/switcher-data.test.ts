// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { closestEntries, workspaceEntries } from "./switcher-data";

afterEach(() => {
  vi.unstubAllGlobals();
  document.body.innerHTML = "";
  delete document.documentElement.dataset.readOnly;
});

const labels = (): string[] => workspaceEntries().map((entry) => entry.label);

it("offers a page's own actions only while their controls exist and can be used", () => {
  expect(labels()).not.toContain("Pin this note");
  document.body.innerHTML = `
    <a data-source-editor-launch href="/edit?key=a">Edit</a>
    <button data-pin-button>Unpin</button>
    <button data-reading-mode>Quiet reading</button>
    <button data-copy-note-url hidden>Copy note URL</button>
    <button data-open-button disabled>Open in external editor</button>
    <button data-shortcuts-open>Keyboard shortcuts</button>`;
  const entries = workspaceEntries();
  expect(entries.find((entry) => entry.label === "Edit this note")).toMatchObject({ path: "/edit?key=a", detail: "Action" });
  // The label follows the control, so a pinned note offers to unpin.
  expect(entries.find((entry) => entry.label === "Unpin this note")).toMatchObject({ click: "[data-pin-button]" });
  expect(labels()).toEqual(expect.arrayContaining(["Toggle quiet reading", "Keyboard shortcuts"]));
  // A hidden or disabled control is an action this page cannot perform.
  expect(labels()).not.toContain("Copy this note’s URL");
  expect(labels()).not.toContain("Open this note in external editor");
});

it("keeps write destinations and write actions out of a read-only workspace", () => {
  expect(labels()).toEqual(expect.arrayContaining(["Threads", "Settings", "History", "Create a note"]));
  document.documentElement.dataset.readOnly = "true";
  for (const label of ["Threads", "Settings", "Sync", "History", "Create a note", "Capture a thought"]) expect(labels()).not.toContain(label);
  expect(labels()).toEqual(expect.arrayContaining(["Library", "Graph", "Properties", "Health"]));
});

it("labels the closest titles as close matches and survives a failed lookup", async () => {
  const fetcher = vi.fn().mockResolvedValueOnce(new Response(JSON.stringify([
    { key: "concepts/deliberate practice", path: "/n/concepts/deliberate%20practice", title: "Deliberate Practice", folder: "Concepts" },
    { key: "inbox", path: "/n/inbox", title: "Inbox", folder: "" },
  ])));
  vi.stubGlobal("fetch", fetcher);
  const signal = new AbortController().signal;
  expect(await closestEntries("delibrate pratice", signal)).toEqual([
    { label: "Deliberate Practice", detail: "Close match · Concepts", path: "/n/concepts/deliberate%20practice" },
    { label: "Inbox", detail: "Close match", path: "/n/inbox" },
  ]);
  expect(String(fetcher.mock.calls[0][0])).toBe("/api/search/suggest?q=delibrate+pratice");

  fetcher.mockResolvedValueOnce(new Response("nope", { status: 500 }));
  expect(await closestEntries("x", signal)).toEqual([]);
});
