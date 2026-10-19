import { test, expect } from "./fixture.js";

export function graphVisuals(snapshot) {
  test("Graph search, keyboard selection and layout disclosure", async ({ page }) => {
    // Keep real Cytoscape rendering and styling; freeze only the layout inputs.
    await page.addInitScript(() => {
      let factory;
      Object.defineProperty(window, "cytoscape", {
        configurable: true,
        get: () => factory,
        set: (value) => {
          factory = new Proxy(value, { apply(target, receiver, args) {
            const graph = Reflect.apply(target, receiver, args);
            const layout = graph.layout.bind(graph);
            graph.layout = () => layout({ name: "preset", fit: false, positions: (node) => {
              const index = ["alpha", "beta", "field", "writing"].indexOf(node.id());
              return { x: 80 + (index % 2) * 180, y: 80 + Math.floor(index / 2) * 160 };
            } });
            return graph;
          } });
        },
      });
    });
    await page.route("**/api/graph.json**", (route) => route.fulfill({ json: {
      nodes: ["alpha", "beta", "field", "writing"].map((id) => ({ data: {
        id, title: { alpha: "Alpha", beta: "Beta", field: "Field journal", writing: "Writing" }[id],
        folder: "", matchesQuery: false,
      } })),
      edges: [["alpha", "beta"], ["alpha", "field"], ["field", "writing"]].map(([source, target], index) => ({ data: { id: `edge-${index}`, source, target } })),
    } }));
    await page.goto("/graph");
    await expect(page.locator("#graph-status")).toContainText("Loaded 4 nodes");
    await expect(page.locator("[data-sync-line]")).toContainText("Review 5 incoming changes");
    const list = page.getByLabel("Notes and folders in this view");
    await list.selectOption("field");
    await page.locator("#graph-canvas").focus();
    await page.keyboard.press("Home");
    await page.locator("h1").click();
    await snapshot(page, "graph");
    await page.locator(".graph-refine-options > summary").click();
    await page.getByText("Graph layout", { exact: true }).click();
    await snapshot(page, "graph-layout");
  });
}
