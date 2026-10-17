import { test, expect } from "./fixture.js";

test("reading loads only the reading bundle within its transfer budget", async ({ page, context }, testInfo) => {
  const session = await context.newCDPSession(page);
  await session.send("Performance.enable");
  await page.goto("/n/Reading.md");
  await expect(page.locator("[data-note-body]")).toBeVisible();
  const scripts = await page.evaluate(() => performance.getEntriesByType("resource")
    .filter((entry) => entry.initiatorType === "script")
    .map((entry) => ({ path: new URL(entry.name).pathname, bytes: entry.encodedBodySize, duration: entry.duration })));
  const { metrics } = await session.send("Performance.getMetrics");
  const measurement = { scripts, scriptDurationSeconds: metrics.find((metric) => metric.name === "ScriptDuration")?.value };
  await testInfo.attach("reading-performance", { body: JSON.stringify(measurement, null, 2), contentType: "application/json" });
  console.log(JSON.stringify(measurement));
  expect(scripts[0].bytes).toBeGreaterThan(0);
  expect(scripts[0].bytes).toBeLessThan(130_000);
  expect(scripts.map((entry) => entry.path)).toEqual(["/static/app.js"]);
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
  await page.goto("/n/Reading.md");
  await expect(page.locator("[data-note-body]")).toBeVisible();
  const search = page.locator("#search-input");
  const started = Date.now();
  await search.fill("Reading");
  await expect(page.locator(".search-result-title").first()).toHaveText("Reading");
  const searchMilliseconds = Date.now() - started;
  const longTasks = await page.evaluate(() => window.readingLongTasks);
  await testInfo.attach("throttled-reading", {
    body: JSON.stringify({ searchMilliseconds, longTasks }), contentType: "application/json",
  });
  // Broad ceilings catch an unusable page without turning machine speed into
  // a benchmark contest. The artifact retains the actual measurements.
  expect(searchMilliseconds).toBeLessThan(3000);
  expect(Math.max(0, ...longTasks)).toBeLessThan(1000);
});
