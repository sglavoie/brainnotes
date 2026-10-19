import { test, expect } from "./fixture.js";

test("reading page stays within its transfer budget", async ({ page, context }, testInfo) => {
  const session = await context.newCDPSession(page);
  await session.send("Performance.enable");
  const navigationStart = Date.now();
  await page.goto("/n/Reading.md");
  await expect(page.locator("[data-note-body]")).toBeVisible();
  const coldNavigationMilliseconds = Date.now() - navigationStart;
  expect(coldNavigationMilliseconds).toBeLessThan(6000);
  const { resources, navigationBytes, firstContentfulPaintMilliseconds } = await page.evaluate(() => {
    const resources = performance.getEntriesByType("resource")
      .map((entry) => ({
        path: new URL(entry.name).pathname,
        type: entry.initiatorType,
        bytes: entry.encodedBodySize,
        duration: entry.duration,
      }));
    return {
      resources,
      navigationBytes: performance.getEntriesByType("navigation")[0]?.encodedBodySize ?? 0,
      firstContentfulPaintMilliseconds: performance.getEntriesByName("first-contentful-paint")[0]?.startTime ?? null,
    };
  });
  const scripts = resources.filter((entry) => entry.type === "script");
  const stylesheets = resources.filter((entry) => entry.type === "link" && entry.path.endsWith(".css"));
  const totalTransferBytes = navigationBytes + resources.reduce((total, entry) => total + entry.bytes, 0);
  const { metrics } = await session.send("Performance.getMetrics");
  const measurement = { navigationBytes, resources, totalTransferBytes, firstContentfulPaintMilliseconds, coldNavigationMilliseconds,
    scriptDurationSeconds: metrics.find((metric) => metric.name === "ScriptDuration")?.value };
  await testInfo.attach("reading-performance", { body: JSON.stringify(measurement, null, 2), contentType: "application/json" });
  console.log(JSON.stringify(measurement));
  expect(scripts[0].bytes).toBeGreaterThan(0);
  expect(scripts[0].bytes).toBeLessThan(130_000);
  expect(scripts.map((entry) => entry.path)).toEqual(["/static/app.js"]);
  expect(stylesheets.map((entry) => entry.path)).toEqual(["/static/app.css"]);
  expect(stylesheets[0].bytes).toBeGreaterThan(0);
  expect(stylesheets[0].bytes).toBeLessThan(220_000);
  expect(navigationBytes).toBeGreaterThan(0);
  expect(totalTransferBytes).toBeLessThan(400_000);
  expect(firstContentfulPaintMilliseconds).not.toBeNull();
});

test("reading and search stay responsive with a slow CPU and connection", async ({ page, context }, testInfo) => {
  const session = await context.newCDPSession(page);
  await session.send("Emulation.setCPUThrottlingRate", { rate: 4 });
  await session.send("Network.enable");
  await session.send("Network.emulateNetworkConditions", {
    offline: false, latency: 100, downloadThroughput: 200_000, uploadThroughput: 100_000,
  });
  await page.addInitScript(() => {
    window.readingLongTasks = [];
    new PerformanceObserver((list) => {
      window.readingLongTasks.push(...list.getEntries().map((entry) => entry.duration));
    }).observe({ type: "longtask", buffered: true });
  });
  const navigationStart = Date.now();
  await page.goto("/n/Reading.md");
  await expect(page.locator("[data-note-body]")).toBeVisible();
  const coldNavigationMilliseconds = Date.now() - navigationStart;
  expect(coldNavigationMilliseconds).toBeLessThan(6000);
  const firstContentfulPaintMilliseconds = await page.evaluate(() =>
    performance.getEntriesByName("first-contentful-paint")[0]?.startTime ?? null);
  expect(firstContentfulPaintMilliseconds).not.toBeNull();
  await page.keyboard.press("/");
  const search = page.locator("#search-input");
  const started = Date.now();
  await search.fill("Reading");
  await expect(page.locator(".search-result-title").first()).toHaveText("Reading");
  const searchMilliseconds = Date.now() - started;
  const longTasks = await page.evaluate(() => window.readingLongTasks);
  const measurement = { coldNavigationMilliseconds, firstContentfulPaintMilliseconds, searchMilliseconds, longTasks };
  await testInfo.attach("throttled-reading", { body: JSON.stringify(measurement), contentType: "application/json" });
  console.log(JSON.stringify(measurement));
  // Broad ceilings catch an unusable page without turning machine speed into
  // a benchmark contest. The artifact retains the actual measurements.
  expect(searchMilliseconds).toBeLessThan(3000);
  expect(Math.max(0, ...longTasks)).toBeLessThan(1000);
});
