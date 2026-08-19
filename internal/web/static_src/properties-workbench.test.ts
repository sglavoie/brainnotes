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

describe("workbench model", () => {
  it("prefers the declared kind over observed kinds", () => {
    expect(editKind(summaryFixture())).toBe("string");
    expect(
      editKind(
        summaryFixture({
          definition: { type: "list", items: "wikilink", required: false, allowed: [], summary: "" },
        })
      )
    ).toBe("list");
    expect(editItemKind(summaryFixture())).toBe("string");
  });

  it("falls back to a single observed kind and then to string", () => {
    const observed = summaryFixture({
      configured: false,
      definition: undefined,
      observed_kinds: ["date"],
    });
    expect(editKind(observed)).toBe("date");

    const mixed = summaryFixture({
      configured: false,
      definition: undefined,
      observed_kinds: ["date", "string"],
    });
    expect(editKind(mixed)).toBe("string");
  });

  it("refuses to seed an editor from an unsupported value", () => {
    expect(
      typedValueFrom(
        { kind: "unsupported", text: "<map>", supported: false, items: [] },
        "string",
        "string"
      )
    ).toBeUndefined();
  });

  it("seeds a list editor from the value elements", () => {
    const seeded = typedValueFrom(
      {
        kind: "list",
        text: "go, web",
        supported: true,
        items: [
          { kind: "string", text: "go", supported: true, items: [] },
          { kind: "string", text: "web", supported: true, items: [] },
        ],
      },
      "list",
      "string"
    );

    expect(seeded?.items?.map((item) => item.text)).toEqual(["go", "web"]);
  });

  it("marks a hashless or unsupported row as uneditable", () => {
    const base = {
      rel_path: "Notes/Alpha.md",
      title: "Alpha",
      folder: "Notes",
      path: "/note/notes/alpha",
      hash: "hash",
      present: true,
      issues: [],
      unsupported: false,
    };
    expect(rowIsEditable(base)).toBe(true);
    expect(rowIsEditable({ ...base, hash: "" })).toBe(false);
    expect(rowIsEditable({ ...base, unsupported: true })).toBe(false);
  });

  it("labels actions and selections", () => {
    expect(actionTitle("replace_value", "status")).toBe("Replace a value of status");
    expect(actionConfirmLabel("rename_property")).toBe("Rename property");
    expect(selectionLabel(0)).toBe("No notes selected.");
    expect(selectionLabel(1)).toBe("1 note selected.");
    expect(selectionLabel(4)).toBe("4 notes selected.");
  });

  it("reports what a plan actually wrote", () => {
    expect(mutationSuccessMessage(mutationFixture({ affected: 0 }))).toBe("Nothing changed.");
    expect(mutationSuccessMessage(mutationFixture({ affected: 2 }))).toBe("Updated 2 notes.");
    expect(
      mutationSuccessMessage(
        mutationFixture({ affected: 0, config_diff: [{ kind: "add", line: 1, text: "x" }] })
      )
    ).toBe("Updated 0 notes and brainnotes.toml.");
    expect(undoToastMessage(mutationFixture())).toBe(
      "Updated 1 note. You can undo this change."
    );
  });
});

describe("typed value controls", () => {
  it("validates every scalar kind", () => {
    expect(scalarValueError("string", " ")).toBe("Enter a value.");
    expect(scalarValueError("date", "2026-13")).toBe("Enter a date as YYYY-MM-DD.");
    expect(scalarValueError("date", "2026-01-02")).toBe("");
    expect(scalarValueError("int", "1.5")).toBe("Enter a whole number.");
    expect(scalarValueError("float", "1.5")).toBe("");
    expect(scalarValueError("bool", "yes")).toBe("Choose true or false.");
    expect(scalarValueError("datetime", "2026-01-02T03:04")).toBe("");
  });

  it("rejects a wikilink that is not a bare target", () => {
    expect(wikilinkTargetError("[[Alpha]]")).toBe("Enter the bare target without [[ ]].");
    expect(wikilinkTargetError("Alpha#Heading")).toBe(
      "A wikilink property cannot carry a heading."
    );
    expect(wikilinkTargetError("Alpha|Name")).toBe(
      "A wikilink property cannot carry an alias."
    );
    expect(wikilinkTargetError("Alpha")).toBe("");
  });

  it("round-trips a datetime through the local control spelling", () => {
    const zoned = datetimeLocalToRFC3339("2026-01-02T03:04:05");
    expect(zoned.startsWith("2026-01-02T03:04:05")).toBe(true);
    expect(rfc3339ToDatetimeLocal(zoned)).toBe("2026-01-02T03:04:05");
  });

  it("collapses an allow-list to a choice", () => {
    const control = createValueControl(document, {
      kind: "string",
      items: "string",
      allowed: ["active", "draft"],
      id: "control",
      label: "status value",
    });

    expect(control.element).toBeInstanceOf(HTMLSelectElement);
    expect(control.read()).toEqual({ kind: "string", text: "active" });
  });

  it("edits a list as repeating scalar rows", () => {
    const control = createValueControl(document, {
      kind: "list",
      items: "string",
      allowed: [],
      value: { kind: "list", text: "", items: [{ kind: "string", text: "go" }] },
      id: "control",
      label: "tags value",
    });
    document.body.appendChild(control.element);

    const add = control.element.querySelector("[data-property-list-add]");
    if (!(add instanceof HTMLButtonElement)) {
      throw new Error("add button missing");
    }
    add.click();
    const inputs = control.element.querySelectorAll("input");
    expect(inputs).toHaveLength(2);
    const second = inputs[1];
    if (!(second instanceof HTMLInputElement)) {
      throw new Error("second row missing");
    }
    second.value = "web";

    expect(control.read()).toEqual({
      kind: "list",
      text: "",
      items: [
        { kind: "string", text: "go" },
        { kind: "string", text: "web" },
      ],
    });

    const removes = control.element.querySelectorAll("[data-property-list-remove]");
    const remove = removes[1];
    if (!(remove instanceof HTMLButtonElement)) {
      throw new Error("remove button missing");
    }
    remove.click();
    expect(control.read().items).toHaveLength(1);
  });
});
