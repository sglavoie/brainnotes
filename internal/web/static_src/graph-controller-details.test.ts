// @vitest-environment jsdom

import { beforeEach, describe, expect, it } from "vitest";

import {
  type GraphDetailsRefs,
  issueItemsForNode,
  renderGraphDetails,
  setOpenNoteTarget,
} from "./graph-controller-details";
import { type GraphNodeModel } from "./graph";

const noteModel = (overrides: Partial<GraphNodeModel> = {}): GraphNodeModel => ({
  id: "Notes/Alpha",
  title: "Alpha",
  folder: "Notes",
  color: "#123456",
  inDegree: 1,
  outDegree: 2,
  degree: 3,
  filteredInDegree: 1,
  filteredOutDegree: 2,
  filteredDegree: 3,
  hasBroken: true,
  brokenCount: 2,
  isOrphan: true,
  isDeadEnd: false,
  matchesQuery: false,
  size: 20,
  ...overrides,
});

const aggregateModel = (): GraphNodeModel =>
  noteModel({
    id: "folder:Notes",
    title: "Notes (4)",
    folder: "Notes",
    isAggregate: true,
    memberIDs: ["Notes/Alpha", "Notes/Beta", "Notes/Gamma", "Notes/Delta"],
  });

let refs: GraphDetailsRefs;

beforeEach(() => {
  document.body.innerHTML = `
    <div class="graph-hover">
      <p id="graph-hover-title"></p>
      <p id="graph-hover-folder"></p>
      <p id="graph-hover-degree"></p>
      <ul id="graph-hover-issues" hidden></ul>
      <a id="graph-open-note"></a>
    </div>
  `;
  refs = {
    detailsPanel: document.querySelector(".graph-hover"),
    hoverTitle: document.getElementById("graph-hover-title"),
    hoverFolder: document.getElementById("graph-hover-folder"),
    hoverDegree: document.getElementById("graph-hover-degree"),
    hoverIssues: document.getElementById("graph-hover-issues"),
    openNote: document.getElementById("graph-open-note") as HTMLAnchorElement,
  };
});

describe("graph details panel", () => {
  it("shows member count and folder for a collapsed folder, not degree or issue flags", () => {
    renderGraphDetails(refs, aggregateModel(), null);

    expect(refs.hoverTitle?.textContent).toBe("Notes (4)");
    expect(refs.hoverFolder?.textContent).toBe("Folder: Notes");
    expect(refs.hoverDegree?.textContent).toBe("Collapsed folder: 4 notes");
    // The member flags copied onto the aggregate model must not surface as its own.
    expect(refs.hoverIssues?.hidden).toBe(true);
    expect(issueItemsForNode(aggregateModel())).toEqual([]);
    expect(refs.detailsPanel?.classList.contains("is-aggregate")).toBe(true);
  });

  it("disables Open note for a collapsed folder and keeps it live for a note", () => {
    renderGraphDetails(refs, aggregateModel(), null);
    expect(refs.openNote?.getAttribute("aria-disabled")).toBe("true");
    expect(refs.openNote?.hasAttribute("href")).toBe(false);

    renderGraphDetails(refs, noteModel(), null);
    expect(refs.openNote?.getAttribute("aria-disabled")).toBe("false");
    expect(refs.openNote?.getAttribute("href")).not.toBe(null);
    expect(refs.hoverDegree?.textContent).toContain("Visible degree: 3");
    expect(refs.detailsPanel?.classList.contains("is-aggregate")).toBe(false);
  });

  it("branches on the explicit flag rather than the id prefix", () => {
    // A real note whose key starts with the aggregate prefix is still a note.
    setOpenNoteTarget(refs.openNote, "folder:Notes", false);
    expect(refs.openNote?.getAttribute("aria-disabled")).toBe("false");

    setOpenNoteTarget(refs.openNote, "Notes/Alpha", true);
    expect(refs.openNote?.getAttribute("aria-disabled")).toBe("true");
  });
});
