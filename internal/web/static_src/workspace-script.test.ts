// @vitest-environment jsdom
import { beforeEach, expect, it } from "vitest";
import { loadWorkspaceScript } from "./workspace-script";

beforeEach(() => document.head.replaceChildren());

it("shares pending and completed loads across independently bundled callers", async () => {
  const first = loadWorkspaceScript("connection-writing");
  const second = loadWorkspaceScript("connection-writing");
  expect(document.scripts).toHaveLength(1);
  document.scripts[0].dispatchEvent(new Event("load"));
  await Promise.all([first, second]);
  await loadWorkspaceScript("connection-writing");
  expect(document.scripts).toHaveLength(1);
});

it("allows a retry after a failed load instead of retaining a rejected receipt", async () => {
  const first = loadWorkspaceScript("connection-dialog");
  const rejected = expect(first).rejects.toThrow("Workspace tools unavailable");
  document.scripts[0].dispatchEvent(new Event("error"));
  await rejected;
  expect(document.scripts).toHaveLength(0);
  const next = loadWorkspaceScript("connection-dialog");
  document.scripts[0].dispatchEvent(new Event("load"));
  await next;
});
