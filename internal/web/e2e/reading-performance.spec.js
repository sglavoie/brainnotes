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
  expect(scripts[0].bytes).toBeLessThan(400_000);
  expect(scripts.map((entry) => entry.path)).toEqual(["/static/app.js"]);
});
