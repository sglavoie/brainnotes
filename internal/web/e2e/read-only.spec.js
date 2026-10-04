// Browser regression for the published read-only profile. Its fixture serves
// the peer vault through external activity state, so using the Brief may write
// beside the vault but no request can mutate the vault itself.

import { readFileSync } from "node:fs";
import { join } from "node:path";

import { expect, gitRead, gitStatus, manifest, test } from "./fixture.js";

test("the read-only Brief browses notes while every document-write door stays closed", async ({
  page,
  request,
}) => {
  const beforeHead = gitRead(manifest.peerVault, "rev-parse", "HEAD").trim();
  expect(gitStatus(manifest.peerVault), "the read-only vault did not start clean").toBe("");

  const response = await page.goto("/");
  expect(response.status()).toBe(200);
  await expect(page.locator("[data-brief-root]")).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.dataset.readOnly)).toBe("true");
  await expect(page.getByRole("link", { name: "Settings", exact: true })).toHaveCount(0);

  const open = page.locator("[data-brief-action-open]").first();
  await expect(open).toBeVisible();
  await open.click();
  await expect(page).toHaveURL(/\/n\//);
  await expect(page.locator("[data-source-editor-launch]")).toBeHidden();

  await page.addInitScript(() => {
    Object.defineProperty(navigator, "clipboard", { value: undefined, configurable: true });
  });
  await page.goto("/library?q=Reading");
  await page.getByRole("button", { name: "Select displayed", exact: true }).click();
  await page.getByRole("button", { name: "Copy wikilinks", exact: true }).click();
  await expect(page.locator("[data-copy-fallback]")).toHaveValue("[[Reading]]");
  await expect(page.locator("[data-overview-bulk]")).toBeHidden();
  await expect(page.locator("[data-pin-button]")).toBeHidden();

  const editor = await request.get("/edit?key=Alpha");
  expect(editor.status()).toBe(403);
  const write = await request.post("/api/source/save", {
    form: {
      key: "Alpha",
      content: "forbidden",
      revision: "forbidden",
      save_id: "read-only-forbidden",
      token: "forbidden",
    },
  });
  expect(write.status()).toBe(403);
  expect(await write.text()).toContain("read-only replica");

  const activity = JSON.parse(
    readFileSync(join(manifest.readOnlyState, ".bnotes", "brief.json"), "utf8"),
  );
  expect(activity.opens.length, "the Brief open was not recorded outside the vault").toBeGreaterThan(
    0,
  );
  expect(gitRead(manifest.peerVault, "rev-parse", "HEAD").trim()).toBe(beforeHead);
  expect(gitStatus(manifest.peerVault), "read-only browsing changed the served vault").toBe("");
});
