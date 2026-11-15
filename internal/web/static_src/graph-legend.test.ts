// @vitest-environment jsdom
import { afterEach, expect, it } from "vitest";
import { normalizeGraphPayload } from "./graph";
import { renderGraphLegend } from "./graph-legend";

afterEach(() => document.body.replaceChildren());

it("names each displayed folder even when colours collide, and replaces stale entries", () => {
  document.body.innerHTML = '<ul id="graph-folder-legend"></ul>';
  const nodes = normalizeGraphPayload({
    nodes: ["", "Books", "Books", "<Notes>"].map((folder, index) => ({
      data: { id: String(index), folder },
    })), edges: [],
  }).nodes;
  for (const node of nodes) node.color = "#709b86";
  renderGraphLegend(nodes);
  const items = [...document.querySelectorAll("li")];
  expect(items.map((item) => item.textContent)).toEqual(["● Vault root", "● <Notes>", "● Books"]);
  expect(document.querySelector("notes")).toBeNull();
  expect(items.every((item) => item.querySelector("span")?.getAttribute("aria-hidden") === "true")).toBe(true);
  expect(items.every((item) => item.querySelector("span")?.style.color === "rgb(112, 155, 134)")).toBe(true);
  renderGraphLegend([{ ...nodes[1], isAggregate: true }]);
  expect(document.querySelectorAll("li")).toHaveLength(1);
  expect(document.querySelector("li")?.textContent).toBe("● Books");
  renderGraphLegend([]);
  expect(document.querySelectorAll("li")).toHaveLength(0);
});
