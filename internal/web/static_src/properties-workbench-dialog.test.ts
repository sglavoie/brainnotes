// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import { createPreviewDialog, previewSummaryLabel } from "./properties-preview-dialog";
import {
  createPropertySchemaEditor,
  definitionError,
  parseAllowedValues,
} from "./properties-schema-editor";

import {
  mountWorkbench,
  mutationFixture,
  requireElement,
  summaryFixture,
} from "./properties-workbench.test-helpers";

afterEach(() => {
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

describe("definition editor", () => {
  it("parses allowed values into a deduplicated list", () => {
    expect(parseAllowedValues(" active \n\ndraft\nactive\n")).toEqual(["active", "draft"]);
  });

  it("rejects a list with no element kind and a scalar with one", () => {
    expect(definitionError({ type: "list", required: false, allowed: [] })).toBe(
      "Choose the element kind a list holds."
    );
    expect(
      definitionError({ type: "string", items: "string", required: false, allowed: [] })
    ).toBe("Only a list declares an element kind.");
    expect(
      definitionError({ type: "list", items: "string", required: false, allowed: [] })
    ).toBe("");
  });

  it("seeds the form from the definition and submits an upsert", () => {
    const root = mountWorkbench();
    const submit = vi.fn();
    const editor = createPropertySchemaEditor({
      root,
      submit,
      setStatus: () => {},
    });
    if (!editor) {
      throw new Error("editor missing");
    }

    editor.sync(summaryFixture());
    const type = requireElement("[data-property-schema-type]", HTMLSelectElement);
    const allowed = requireElement("[data-property-schema-allowed]", HTMLTextAreaElement);
    const required = requireElement("[data-property-schema-required]", HTMLInputElement);
    expect(type.value).toBe("string");
    expect(required.checked).toBe(true);
    expect(allowed.value).toBe("active\ndraft");

    requireElement("[data-property-schema-save]", HTMLButtonElement).click();
    expect(submit).toHaveBeenCalledWith("upsert", {
      type: "string",
      items: undefined,
      required: true,
      allowed: ["active", "draft"],
    });
  });

  it("only seeds the form when an inferred definition is adopted", () => {
    const root = mountWorkbench();
    const submit = vi.fn();
    const setStatus = vi.fn();
    const editor = createPropertySchemaEditor({ root, submit, setStatus });
    if (!editor) {
      throw new Error("editor missing");
    }

    editor.sync(
      summaryFixture({
        configured: false,
        definition: undefined,
        proposal: {
          property: "status",
          type: "list",
          items: "string",
          notes: 2,
          rationale: "All 2 notes store a list of string values.",
          toml: "[frontmatter.properties.status]",
        },
      })
    );

    const adopt = requireElement("[data-property-schema-adopt]", HTMLButtonElement);
    const remove = requireElement("[data-property-schema-delete]", HTMLButtonElement);
    expect(adopt.hidden).toBe(false);
    expect(remove.disabled).toBe(true);

    adopt.click();
    expect(submit).not.toHaveBeenCalled();
    expect(setStatus).toHaveBeenCalled();
    expect(requireElement("[data-property-schema-type]", HTMLSelectElement).value).toBe("list");
    expect(requireElement("[data-property-schema-items]", HTMLSelectElement).value).toBe(
      "string"
    );
    expect(requireElement("[data-property-schema-required]", HTMLInputElement).checked).toBe(
      false
    );
  });
});

describe("preview dialog", () => {
  it("refuses to confirm a blocked plan", () => {
    const host = document.createElement("div");
    document.body.appendChild(host);
    const dialog = createPreviewDialog(document, host);
    const onConfirm = vi.fn();

    dialog.open({
      title: "Remove status",
      summary: "1 note selected, 1 file would change.",
      confirmLabel: "Remove property",
      result: mutationFixture({
        preview_id: "",
        blockers: [
          {
            rel_path: "Notes/Alpha.md",
            property: "status",
            kind: "parse_error",
            detail: "frontmatter does not parse",
            fatal: true,
          },
        ],
      }),
      onConfirm,
    });

    const confirm = requireElement("[data-property-preview-confirm]", HTMLButtonElement);
    expect(confirm.disabled).toBe(true);
    expect(
      requireElement("[data-property-preview-expiry]", HTMLElement).textContent
    ).toBe("This plan cannot be applied until every blocker is resolved.");
    expect(
      requireElement("[data-property-preview-blockers]", HTMLElement).textContent
    ).toContain("frontmatter does not parse");
  });

  it("shows the configuration diff, the projection, and the receipt expiry", () => {
    const host = document.createElement("div");
    document.body.appendChild(host);
    const now = new Date("2026-01-02T00:00:00Z");
    const dialog = createPreviewDialog(document, host, { now: () => now });

    dialog.open({
      title: "Define status",
      summary: "",
      confirmLabel: "Save definition",
      result: mutationFixture({
        config_diff: [
          { kind: "add", line: 1, text: "[frontmatter.properties.status]" },
          { kind: "context", line: 2, text: "type = \"string\"" },
        ],
        expires_at: "2026-01-02T00:05:00Z",
      }),
      onConfirm: () => {},
    });

    expect(
      requireElement("[data-property-preview-config]", HTMLElement).textContent
    ).toContain("+ [frontmatter.properties.status]");
    expect(
      requireElement("[data-property-preview-projection]", HTMLElement).textContent
    ).toBe("Violations 1 -> 2.");
    expect(requireElement("[data-property-preview-expiry]", HTMLElement).textContent).toBe(
      "This preview expires in about 5 minutes."
    );
    expect(
      requireElement("[data-property-preview-files]", HTMLElement).textContent
    ).toContain("Notes/Alpha.md");
  });
});

describe("preview dialog dismissal and summary", () => {
  it("cancels on escape and restores focus to the opener", () => {
    const host = document.createElement("div");
    const opener = document.createElement("button");
    document.body.append(opener, host);
    opener.focus();

    const dialog = createPreviewDialog(document, host);
    const onCancel = vi.fn();
    dialog.open({
      title: "Remove status",
      summary: "",
      confirmLabel: "Remove property",
      result: mutationFixture(),
      onCancel,
      onConfirm: () => {},
    });
    expect(dialog.isOpen()).toBe(true);

    requireElement("[data-property-preview-confirm]", HTMLButtonElement).dispatchEvent(
      new KeyboardEvent("keydown", { key: "Escape", bubbles: true })
    );

    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(dialog.isOpen()).toBe(false);
    expect(document.activeElement).toBe(opener);
  });

  it("summarizes a plan that also rewrites the configuration", () => {
    expect(previewSummaryLabel(mutationFixture())).toBe(
      "1 note selected, 1 file would change."
    );
    expect(
      previewSummaryLabel(
        mutationFixture({
          selected: 0,
          config_diff: [{ kind: "add", line: 1, text: "x" }],
        })
      )
    ).toBe("1 file would change, plus brainnotes.toml.");
  });
});


