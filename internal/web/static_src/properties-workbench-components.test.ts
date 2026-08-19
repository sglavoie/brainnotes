// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import type { PropertyMutationResult, PropertySummary } from "./api";
import { initPropertyDetail } from "./properties";
import {
  configInvalidMessage,
  configRestartMessage,
  createConfigStatusPoller,
} from "./properties-config-status";
import { createPreviewDialog, previewSummaryLabel } from "./properties-preview-dialog";
import { renderRows } from "./properties-render";
import {
  createPropertySchemaEditor,
  definitionError,
  parseAllowedValues,
} from "./properties-schema-editor";
import { createUndoToast } from "./properties-toast";
import {
  createValueControl,
  datetimeLocalToRFC3339,
  rfc3339ToDatetimeLocal,
  scalarValueError,
  wikilinkTargetError,
} from "./properties-value-control";
import {
  actionConfirmLabel,
  actionTitle,
  editItemKind,
  editKind,
  mutationSuccessMessage,
  rowIsEditable,
  selectionLabel,
  typedValueFrom,
  undoToastMessage,
} from "./properties-workbench-model";

import {
  detailPayload,
  jsonResponse,
  mountWorkbench,
  mutationFixture,
  requireElement,
  routedFetch,
  submitEditor,
  summaryFixture,
  waitForExpect,
} from "./properties-workbench.test-helpers";

afterEach(() => {
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

describe("configuration status", () => {
  it("explains an invalid configuration and a restart-required key", () => {
    expect(
      configInvalidMessage({
        generation: 3,
        source_hash: "abc",
        status: "ok",
        restart_required: [],
        writable: true,
      })
    ).toBe("");
    expect(
      configInvalidMessage({
        generation: 3,
        source_hash: "abc",
        status: "invalid",
        error: "line 4",
        restart_required: [],
        writable: true,
      })
    ).toContain("brainnotes.toml is invalid: line 4");
    expect(
      configRestartMessage({
        generation: 3,
        source_hash: "abc",
        status: "ok",
        restart_required: ["ignore"],
        writable: true,
      })
    ).toBe("ignore changed on disk. Restart bn serve to apply it.");
  });

  it("does not poll while the page is hidden", async () => {
    const fetchMock = vi.fn(() =>
      Promise.resolve(
        jsonResponse({
          ok: true,
          generation: 4,
          source_hash: "abc",
          status: "ok",
          restart_required: [],
          writable: true,
        })
      )
    );
    const onStatus = vi.fn();
    const hidden = { visibilityState: "hidden", addEventListener: () => {} } as unknown as Document;
    const poller = createConfigStatusPoller(hidden, { fetch: fetchMock, onStatus });

    await poller.poll();

    expect(fetchMock).not.toHaveBeenCalled();
    expect(onStatus).not.toHaveBeenCalled();
  });

  it("reports a generation change only after a first observed generation", async () => {
    let generation = 4;
    const fetchMock = vi.fn(() =>
      Promise.resolve(
        jsonResponse({
          ok: true,
          generation,
          source_hash: "abc",
          status: "ok",
          restart_required: [],
          writable: true,
        })
      )
    );
    const onGenerationChange = vi.fn();
    const poller = createConfigStatusPoller(document, {
      fetch: fetchMock,
      onStatus: () => {},
      onGenerationChange,
    });

    await poller.poll();
    expect(onGenerationChange).not.toHaveBeenCalled();

    generation = 5;
    await poller.poll();
    expect(onGenerationChange).toHaveBeenCalledWith(5);
    expect(poller.latest()?.generation).toBe(5);
  });
});

describe("undo toast", () => {
  it("offers undo once and hides itself after it is used", () => {
    const host = document.createElement("div");
    document.body.appendChild(host);
    const onUndo = vi.fn();
    const toast = createUndoToast(document, host, { onUndo });

    toast.show({
      message: "Updated 1 note. You can undo this change.",
      journalID: "entry-7",
      historyHref: "#property-history",
    });
    expect(toast.element.hidden).toBe(false);

    const history = requireElement("[data-property-toast-history]", HTMLAnchorElement);
    expect(history.getAttribute("href")).toBe("#property-history");

    requireElement("[data-property-toast-undo]", HTMLButtonElement).click();
    expect(onUndo).toHaveBeenCalledWith("entry-7");
    expect(toast.element.hidden).toBe(true);

    requireElement("[data-property-toast-undo]", HTMLButtonElement).click();
    expect(onUndo).toHaveBeenCalledTimes(1);
  });
});

describe("row rendering with controls", () => {
  it("renders no control without options", () => {
    const list = document.createElement("ul");
    renderRows(list, [
      {
        rel_path: "Notes/Alpha.md",
        title: "Alpha",
        folder: "Notes",
        path: "/note/notes/alpha",
        hash: "hash",
        present: true,
        issues: [],
        unsupported: false,
      },
    ]);

    expect(list.querySelectorAll("button")).toHaveLength(0);
    expect(list.querySelectorAll("input")).toHaveLength(0);
  });

  it("disables editing for an unsupported or missing row", () => {
    const list = document.createElement("ul");
    renderRows(
      list,
      [
        {
          rel_path: "Notes/Alpha.md",
          title: "Alpha",
          folder: "Notes",
          path: "/note/notes/alpha",
          hash: "hash",
          present: true,
          issues: [],
          unsupported: true,
        },
        {
          rel_path: "Notes/Beta.md",
          title: "Beta",
          folder: "Notes",
          path: "/note/notes/beta",
          hash: "hash",
          present: false,
          issues: [],
          unsupported: false,
        },
      ],
      { selected: new Set(["Notes/Beta.md"]), listKind: true }
    );

    const edits = list.querySelectorAll<HTMLButtonElement>("[data-property-row-edit]");
    expect(edits[0]?.disabled).toBe(true);
    expect(edits[1]?.disabled).toBe(true);
    expect(list.querySelectorAll("[data-property-row-add]")).toHaveLength(2);
    const unsets = list.querySelectorAll<HTMLButtonElement>("[data-property-row-unset]");
    expect(unsets[1]?.disabled).toBe(true);
    expect(list.querySelectorAll(".is-selected")).toHaveLength(1);
  });
});


