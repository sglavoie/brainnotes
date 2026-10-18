import { test, expect } from "./fixture.js";
import { SURFACE } from "./editor-helpers.js";

async function throttle(page, context) {
  const session = await context.newCDPSession(page);
  await session.send("Emulation.setCPUThrottlingRate", { rate: 4 });
  await session.send("Network.enable");
  await session.send("Network.emulateNetworkConditions", {
    offline: false, latency: 100, downloadThroughput: 200_000, uploadThroughput: 100_000,
  });
  await page.addInitScript(() => {
    window.performanceTasks = [];
    new PerformanceObserver((list) => {
      window.performanceTasks.push(...list.getEntries().map((entry) => entry.duration));
    }).observe({ type: "longtask", buffered: true });
  });
}

async function report(page, testInfo, measurements) {
  const longTasks = await page.evaluate(() => window.performanceTasks);
  const result = { noteCount: testInfo.project.metadata.noteCount, ...measurements, longTasks };
  console.log(JSON.stringify(result));
  await testInfo.attach("vault-performance", { body: JSON.stringify(result, null, 2), contentType: "application/json" });
  expect(Math.max(0, ...longTasks)).toBeLessThan(250);
}

test("large vault Library search and filtering remain responsive", async ({ page, context }, testInfo) => {
  await throttle(page, context);
  await page.goto("/library?q=observation&sort=path");
  await expect(page.locator(".overview-result-link")).toHaveCount(50);
  await page.evaluate(() => { window.performanceTasks = []; });
  const samples = [];
  for (const query of ["Record000", "Record001", "Record002", "Record003", "observation"]) {
    const response = page.waitForResponse((r) => r.url().includes("/api/search?") && new URL(r.url()).searchParams.get("q") === query);
    const start = Date.now();
    await page.locator("#search-input").fill(query);
    await response;
    await expect(page.locator("[data-overview-search-results]")).toHaveAttribute("aria-busy", "false");
    samples.push(Date.now() - start);
  }
  const filterStart = Date.now();
  await page.locator("[data-library-expression] > summary").click();
  await page.locator("[data-query-bar-input]").fill("status:draft");
  await expect(page.locator("[data-overview-search-status]")).toContainText(`of ${testInfo.project.metadata.noteCount > 10000 ? 5000 : 500}`);
  const filterMilliseconds = Date.now() - filterStart;
  await report(page, testInfo, { searchMilliseconds: samples, filterMilliseconds });
  expect(Math.max(...samples)).toBeLessThan(1500);
  expect(filterMilliseconds).toBeLessThan(1500);
});

test("long note editing remains responsive", async ({ page, context }, testInfo) => {
  await throttle(page, context);
  const navigationStart = Date.now();
  await page.goto("/edit?key=LongWriting");
  const editor = page.locator(SURFACE);
  await expect(editor).toContainText("The morning trail");
  const coldNavigationMilliseconds = Date.now() - navigationStart;
  await editor.click();
  await page.evaluate(() => { window.performanceTasks = []; });
  const samples = [];
  for (let i = 0; i < 5; i++) {
    const start = Date.now();
    await page.keyboard.type("A measured observation. ");
    await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    samples.push(Date.now() - start);
  }
  const saveStart = Date.now();
  await page.locator("[data-source-save]").click();
  await expect(page.locator("[data-source-status]")).toContainText("Saved to vault");
  const saveConfirmationMilliseconds = Date.now() - saveStart;
  await report(page, testInfo, { typingMilliseconds: samples, coldNavigationMilliseconds, saveConfirmationMilliseconds });
  expect(coldNavigationMilliseconds).toBeLessThan(8000);
  expect(saveConfirmationMilliseconds).toBeLessThan(3000);
  expect(Math.max(...samples)).toBeLessThan(500);
});

test("result pages preserve selections and the return destination", async ({ page }) => {
  await page.goto("/library?q=observation&sort=path&limit=65");
  await expect(page.locator(".overview-result-link")).toHaveCount(50);
  await page.locator(".overview-result-select input").first().check();
  await page.getByRole("button", { name: "Next results", exact: true }).click();
  await expect(page.locator(".overview-result-link")).toHaveCount(15);
  await expect(page.locator("[data-overview-selected-count]")).toHaveText("1 selected (1 on other pages)");
  await expect(page.getByRole("button", { name: "Next results", exact: true })).toBeDisabled();
  await page.locator(".overview-result-select input").first().check();
  await page.getByRole("button", { name: "Previous results", exact: true }).click();
  await expect(page.locator(".overview-result-select input").first()).toBeChecked();
  await expect(page.locator("[data-overview-selected-count]")).toHaveText("2 selected (1 on other pages)");
  await page.getByRole("button", { name: "Next results", exact: true }).click();
  await expect(page.locator(".overview-result-link")).toHaveCount(15);
  await page.locator(".overview-result-link").first().click();
  await page.getByRole("link", { name: "Back to results", exact: true }).click();
  await expect(page.locator(".overview-result-title").first()).toHaveText("Record00050");
  await expect(page.locator(".overview-result-link").first()).toBeFocused();
  expect(new URL(page.url()).searchParams.get("offset")).toBe("50");
  await expect(page.locator(".overview-result-select input").first()).toBeChecked();
  await expect(page.locator("[data-overview-selected-count]")).toHaveText("2 selected (1 on other pages)");
  await page.getByRole("button", { name: "Previous results", exact: true }).click();
  await expect(page.locator(".overview-result-select input").first()).toBeChecked();
});
