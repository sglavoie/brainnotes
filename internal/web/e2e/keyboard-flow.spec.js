import { test, expect } from "./fixture.js";

const unfocus = (page) => page.evaluate(() => document.activeElement?.blur());
const sheet = (page) => page.getByRole("dialog", { name: "Keyboard shortcuts" });
const chord = async (page, key) => { await page.keyboard.press("g"); await page.keyboard.press(key); };

for (const width of [1280, 390]) {
  test(`keyboard navigation, list selection and editing at ${width}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 });
    await page.goto("/n/Alpha");
    await chord(page, "l");
    await expect(page).toHaveURL(/\/library$/);
    await page.locator("#search-input").fill("field");
    await unfocus(page);
    const links = page.locator(".overview-result-link");
    await expect(links.first()).toBeVisible();
    await page.keyboard.press("j");
    await expect(links.nth(0)).toBeFocused();
    await page.keyboard.press("j");
    await expect(links.nth(1)).toBeFocused();
    await page.keyboard.press("k");
    await expect(links.nth(0)).toBeFocused();
    const checkbox = page.locator(".overview-result-item").first().getByRole("checkbox");
    await page.keyboard.press("x");
    await expect(checkbox).toBeChecked();
    await expect(links.first()).toBeFocused();
    await page.keyboard.press("x");
    await expect(checkbox).not.toBeChecked();
    const destination = await links.first().getAttribute("href");
    await page.keyboard.press("Enter");
    await expect(page).toHaveURL(new RegExp(destination.replace(/[.*+?^${}()|[\]\\]/g, "\\$&") + "$"));
    await expect(page.locator("[data-note-body]")).toBeVisible();
    await page.keyboard.press("e");
    await expect(page).toHaveURL(/\/edit\?key=/);
    await expect(page.locator(".cm-content")).toBeVisible();
    await page.locator(".cm-content").focus();
    await page.keyboard.type("g l?");
    await expect(sheet(page)).toBeHidden();
    await expect(page).toHaveURL(/\/edit\?key=/);
  });
}

test("typing, native controls, Graph and dialogs retain their keys", async ({ page }) => {
  await page.goto("/library");
  const input = page.locator("#search-input");
  await input.fill("");
  await input.pressSequentially("g l");
  await expect(input).toHaveValue("g l");
  await expect(page).toHaveURL(/\/library/);
  await page.goto("/graph");
  const canvas = page.locator("#graph-canvas");
  await canvas.focus();
  await chord(page, "l");
  await page.keyboard.press("?");
  await page.keyboard.press("/");
  await expect(canvas).toBeFocused();
  await expect(page).toHaveURL(/\/graph$/);
  await page.locator("#graph-node-list").focus();
  await chord(page, "l");
  await expect(page).toHaveURL(/\/graph$/);
  await unfocus(page);
  await page.keyboard.press("?");
  await expect(sheet(page)).toBeVisible();
  await chord(page, "l");
  await expect(page).toHaveURL(/\/graph$/);
  await page.keyboard.press("Escape");
  await page.locator("[data-shortcuts-open]").last().click();
  await expect(sheet(page)).toBeVisible();
  await sheet(page).getByRole("button", { name: "Close", exact: true }).click();
  await expect(page.locator("[data-shortcuts-open]").last()).toBeFocused();
});

test("the persistent off switch disables single keys and retains modifier shortcuts", async ({ page }) => {
  await page.goto("/n/Alpha");
  await page.locator(".source-editor-workspace > summary").click();
  await page.getByLabel("Single-key shortcuts", { exact: true }).uncheck();
  await expect.poll(() => page.evaluate(() => localStorage.getItem("bn.shortcuts"))).toBe("off");
  await page.reload();
  await unfocus(page);
  for (const key of ["g", "l", "e", "?", "/"]) await page.keyboard.press(key);
  await expect(page).toHaveURL(/\/n\/Alpha$/);
  await expect(sheet(page)).toBeHidden();
  await expect(page.locator("#search-input")).not.toBeFocused();
  await page.keyboard.press("ControlOrMeta+k");
  await expect(page.locator("#switcher-input")).toBeFocused();
  await page.keyboard.press("Escape");
  await page.keyboard.press("ControlOrMeta+Shift+Space");
  await expect(page.locator(".capture-dialog")).toBeVisible();
  await page.keyboard.press("Escape");
  await page.goto("/library?q=field");
  await expect(page.locator(".overview-result-link").first()).toBeVisible();
  await page.keyboard.press("j");
  await page.keyboard.press("k");
  await expect(page.locator(".overview-result-link").first()).not.toBeFocused();
  await page.locator(".source-editor-workspace > summary").click();
  await page.getByRole("button", { name: "Keyboard shortcuts", exact: true }).click();
  await expect(sheet(page)).toBeVisible();
  for (const heading of ["Move around", "Search and references", "Graph and Pinned", "Editor"]) {
    await expect(sheet(page).getByRole("heading", { name: heading, exact: true })).toBeVisible();
  }
});

test("keyboard tools load once, recover from a failed load, and return focus", async ({ page }) => {
  let attempts = 0;
  await page.route("**/static/keyboard-tools.js", async (route) => {
    attempts++;
    if (attempts === 1) await route.abort();
    else await route.continue();
  });
  await page.goto("/n/Alpha");
  const trigger = page.locator("[data-switcher-launch]");
  await trigger.click();
  await expect(page.getByRole("status").filter({ hasText: "Keyboard tools unavailable" })).toBeVisible();
  await expect(trigger).toBeFocused();
  await trigger.click();
  await expect(page.locator("#switcher-input")).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(trigger).toBeFocused();
  await page.keyboard.press("?");
  await expect(sheet(page)).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(trigger).toBeFocused();
  await page.keyboard.press("ControlOrMeta+k");
  await expect(page.locator("#switcher-input")).toBeFocused();
  await expect(page.locator("#switcher-input")).toHaveCount(1);
  await expect(page.locator("[data-shortcuts-sheet]")).toHaveCount(1);
  expect(attempts).toBe(2);
});

test("Pinned and Threads use their existing row links", async ({ page }) => {
  await page.goto("/n/Alpha");
  const pin = page.locator("[data-pin-note] [data-pin-button]");
  if (await pin.getAttribute("aria-pressed") === "false") await pin.click();
  await expect(pin).toHaveAttribute("aria-pressed", "true");
  await chord(page, "p");
  await expect(page).toHaveURL(/\/pinned$/);
  const pinned = page.locator(".pinned-note-title");
  await page.keyboard.press("j");
  await expect(pinned.first()).toBeFocused();
  const destination = new URL(await pinned.first().getAttribute("href"), page.url()).href;
  const title = await pinned.first().innerText();
  await page.keyboard.press("o");
  // Other journeys can pin an empty note; opening its exact row is the contract.
  await expect(page).toHaveURL(destination);
  await expect(page.getByRole("heading", { level: 1, name: title, exact: true })).toBeVisible();
  await page.goto("/threads");
  const details = page.locator("details").filter({ has: page.locator("[data-new-note]") });
  await details.locator("summary").click();
  await page.getByLabel("Thread title", { exact: true }).fill("Keyboard flow thread");
  await page.getByLabel("Question", { exact: true }).fill("Can I resume this with the keyboard?");
  await page.getByRole("button", { name: "Create thread", exact: true }).click();
  await expect(page).toHaveURL(/\/edit\?key=/);
  await page.goto("/threads");
  const threads = page.locator("[data-thread-link]");
  await page.keyboard.press("k");
  await expect(threads.last()).toBeFocused();
  await page.keyboard.press("o");
  await expect(page).toHaveURL(/\/edit\?key=/);
});

for (const width of [1280, 390]) for (const path of ["/n/Alpha", "/edit?key=Alpha", "/broken"]) {
  test(`shortcut controls remain available on ${path} at ${width}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 });
    await page.goto(path);
    const workspace = page.locator(".source-editor-workspace > summary");
    await workspace.click();
    const toggle = page.getByLabel("Single-key shortcuts", { exact: true });
    await toggle.uncheck();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.screenshot({ path: test.info().outputPath("shortcut-controls.png") });
    await page.getByRole("button", { name: "Keyboard shortcuts", exact: true }).click();
    await expect(sheet(page)).toContainText("Alt Enter writes a thought");
    await expect(sheet(page)).toContainText("Control Space requests query-expression suggestions");
    await page.keyboard.press("Escape");
    await expect(workspace).toBeFocused();
    await unfocus(page);
    await chord(page, "l");
    await expect(page).toHaveURL(new RegExp(path.replace("?", "\\?") + "$"));
    await workspace.click();
    await toggle.check();
    await workspace.click();
    await unfocus(page);
    await chord(page, "l");
    await expect(page).toHaveURL(/\/library$/);
  });
}
