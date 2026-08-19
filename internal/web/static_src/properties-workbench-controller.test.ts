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

describe("property detail workbench", () => {
  it("renders no mutation control without a CSRF token", async () => {
    const fetchMock = routedFetch({ "/api/properties/detail": () => detailPayload() });
    mountWorkbench("");
    initPropertyDetail({ fetch: fetchMock });

    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-property-row]")).toHaveLength(2);
    });
    expect(document.querySelectorAll("[data-property-row-edit]")).toHaveLength(0);
    expect(document.querySelectorAll("[data-property-row-select]")).toHaveLength(0);
  });

  it("saves a single-note replacement immediately and offers undo", async () => {
    const fetchMock = routedFetch({
      "/api/properties/detail": () => detailPayload(),
      "/api/properties/value": () =>
        mutationFixture({
          action: "set",
          preview_id: "",
          expires_at: "",
          journal_id: "entry-9",
        }),
    });
    mountWorkbench();
    initPropertyDetail({ fetch: fetchMock });

    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-property-row-edit]")).toHaveLength(2);
    });

    requireElement(
      "[data-property-row-edit='Notes/Alpha.md']",
      HTMLButtonElement
    ).click();
    const control = requireElement("[data-property-control]", HTMLSelectElement);
    control.value = "draft";
    submitEditor();

    await waitForExpect(() => {
      expect(
        fetchMock.mock.calls.some(([url]) => String(url) === "/api/properties/value")
      ).toBe(true);
    });

    const call = fetchMock.mock.calls.find(
      ([url]) => String(url) === "/api/properties/value"
    );
    const init = call?.[1];
    expect(init?.method).toBe("POST");
    expect(
      (init?.headers as Record<string, string> | undefined)?.["X-BN-CSRF"]
    ).toBe("tok");
    expect(JSON.parse(String(init?.body))).toEqual({
      note: "Notes/Alpha.md",
      property: "status",
      op: "replace",
      expected_hash: "hash-alpha",
      value: { kind: "string", text: "draft" },
    });

    await waitForExpect(() => {
      expect(requireElement("[data-property-toast]", HTMLElement).hidden).toBe(false);
    });
    expect(requireElement("[data-property-toast-message]", HTMLElement).textContent).toBe(
      "Updated 1 note. You can undo this change."
    );
  });

  it("previews a removal before it applies it", async () => {
    const fetchMock = routedFetch({
      "/api/properties/detail": () => detailPayload(),
      "/api/properties/preview": () => mutationFixture(),
      "/api/properties/apply": () =>
        mutationFixture({ preview_id: "", expires_at: "", journal_id: "entry-3" }),
    });
    mountWorkbench();
    initPropertyDetail({ fetch: fetchMock });

    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-property-row-unset]")).toHaveLength(2);
    });

    requireElement(
      "[data-property-row-unset='Notes/Alpha.md']",
      HTMLButtonElement
    ).click();

    await waitForExpect(() => {
      expect(requireElement("[data-property-dialog]", HTMLElement).hidden).toBe(false);
    });
    const previewCall = fetchMock.mock.calls.find(
      ([url]) => String(url) === "/api/properties/preview"
    );
    expect(JSON.parse(String(previewCall?.[1]?.body))).toEqual({
      action: "unset",
      property: "status",
      notes: ["Notes/Alpha.md"],
    });

    requireElement("[data-property-preview-confirm]", HTMLButtonElement).click();

    await waitForExpect(() => {
      const applyCall = fetchMock.mock.calls.find(
        ([url]) => String(url) === "/api/properties/apply"
      );
      expect(JSON.parse(String(applyCall?.[1]?.body))).toEqual({ preview_id: "receipt-1" });
    });
  });

  it("undoes an applied change through the unified history endpoint", async () => {
    const fetchMock = routedFetch({
      "/api/properties/detail": () => detailPayload(),
      "/api/properties/value": () =>
        mutationFixture({ preview_id: "", expires_at: "", journal_id: "entry-9" }),
      "/api/undo": () => ({ ok: true }),
    });
    mountWorkbench();
    initPropertyDetail({ fetch: fetchMock });

    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-property-row-edit]")).toHaveLength(2);
    });
    requireElement(
      "[data-property-row-edit='Notes/Alpha.md']",
      HTMLButtonElement
    ).click();
    submitEditor();

    await waitForExpect(() => {
      expect(requireElement("[data-property-toast]", HTMLElement).hidden).toBe(false);
    });
    requireElement("[data-property-toast-undo]", HTMLButtonElement).click();

    await waitForExpect(() => {
      const undoCall = fetchMock.mock.calls.find(([url]) => String(url) === "/api/undo");
      expect(String(undoCall?.[1]?.body)).toBe("token=tok&id=entry-9");
    });
  });

  it("previews a multi-note change from the selection", async () => {
    const fetchMock = routedFetch({
      "/api/properties/detail": () => detailPayload(),
      "/api/properties/preview": () => mutationFixture({ action: "set", selected: 2 }),
    });
    mountWorkbench();
    initPropertyDetail({ fetch: fetchMock });

    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-property-row]")).toHaveLength(2);
    });

    requireElement("[data-property-select-all]", HTMLButtonElement).click();
    expect(requireElement("[data-property-selection-count]", HTMLElement).textContent).toBe(
      "2 notes selected."
    );

    const setButton = requireElement("[data-property-action='set']", HTMLButtonElement);
    expect(setButton.disabled).toBe(false);
    setButton.click();

    const control = requireElement("[data-property-control]", HTMLSelectElement);
    control.value = "draft";
    submitEditor();

    await waitForExpect(() => {
      const previewCall = fetchMock.mock.calls.find(
        ([url]) => String(url) === "/api/properties/preview"
      );
      expect(JSON.parse(String(previewCall?.[1]?.body))).toEqual({
        action: "set",
        property: "status",
        notes: ["Notes/Alpha.md", "Notes/Beta.md"],
        value: { kind: "string", text: "draft" },
      });
    });
  });

  it("keeps replace value unavailable until a value filter is active", async () => {
    const fetchMock = routedFetch({
      "/api/properties/detail": () => detailPayload({ filter: { status: "all", value: "active" } }),
    });
    mountWorkbench();
    initPropertyDetail({ fetch: fetchMock });

    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-property-row]")).toHaveLength(2);
    });
    requireElement("[data-property-select-all]", HTMLButtonElement).click();
    expect(
      requireElement("[data-property-action='replace_value']", HTMLButtonElement).disabled
    ).toBe(false);
  });

  it("drives selection and editing from the keyboard", async () => {
    const fetchMock = routedFetch({ "/api/properties/detail": () => detailPayload() });
    mountWorkbench();
    initPropertyDetail({ fetch: fetchMock });

    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-property-row]")).toHaveLength(2);
    });

    document.body.dispatchEvent(new KeyboardEvent("keydown", { key: "/", bubbles: true }));
    expect(document.activeElement).toBe(
      requireElement("[data-property-filter-query]", HTMLInputElement)
    );

    const rows = document.querySelectorAll<HTMLElement>("[data-property-row]");
    const first = rows[0];
    const second = rows[1];
    if (!first || !second) {
      throw new Error("rows missing");
    }
    first.focus();
    first.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true }));
    expect(document.activeElement).toBe(second);

    second.dispatchEvent(new KeyboardEvent("keydown", { key: " ", bubbles: true }));
    expect(requireElement("[data-property-selection-count]", HTMLElement).textContent).toBe(
      "1 note selected."
    );

    second.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    expect(document.querySelectorAll("[data-property-editor-form]")).toHaveLength(1);

    document.body.dispatchEvent(
      new KeyboardEvent("keydown", { key: "Escape", bubbles: true })
    );
    expect(document.querySelectorAll("[data-property-editor-form]")).toHaveLength(0);

    document.body.dispatchEvent(
      new KeyboardEvent("keydown", { key: "Escape", bubbles: true })
    );
    expect(requireElement("[data-property-selection-count]", HTMLElement).textContent).toBe(
      "No notes selected."
    );
  });

  it("previews a definition change and shows the configuration banners", async () => {
    const fetchMock = routedFetch({
      "/api/properties/detail": () =>
        detailPayload({
          config: {
            generation: 3,
            source_hash: "abc",
            status: "invalid",
            error: "line 4",
            restart_required: ["ignore"],
            writable: false,
          },
        }),
      "/api/properties/schema/preview": () =>
        mutationFixture({
          action: "schema_upsert",
          config_diff: [{ kind: "add", line: 1, text: "[frontmatter.properties.status]" }],
        }),
    });
    mountWorkbench();
    initPropertyDetail({ fetch: fetchMock });

    await waitForExpect(() => {
      expect(requireElement("[data-property-config-invalid]", HTMLElement).hidden).toBe(false);
    });
    expect(
      requireElement("[data-property-config-restart]", HTMLElement).textContent
    ).toContain("Restart bn serve");

    requireElement("[data-property-schema-save]", HTMLButtonElement).click();

    await waitForExpect(() => {
      const schemaCall = fetchMock.mock.calls.find(
        ([url]) => String(url) === "/api/properties/schema/preview"
      );
      expect(JSON.parse(String(schemaCall?.[1]?.body))).toEqual({
        op: "upsert",
        property: "status",
        definition: {
          type: "string",
          required: true,
          allowed: ["active", "draft"],
        },
      });
    });
  });

  it("reports a refused change without clearing the rows", async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.startsWith("/api/properties/detail")) {
        return Promise.resolve(jsonResponse(detailPayload()));
      }
      return Promise.resolve(
        jsonResponse({ ok: false, code: "conflict", message: "note changed on disk" }, 409)
      );
    });
    mountWorkbench();
    initPropertyDetail({ fetch: fetchMock });

    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-property-row-edit]")).toHaveLength(2);
    });
    requireElement(
      "[data-property-row-edit='Notes/Alpha.md']",
      HTMLButtonElement
    ).click();
    submitEditor();

    await waitForExpect(() => {
      expect(requireElement("[data-property-status]", HTMLElement).textContent).toContain(
        "note changed on disk"
      );
    });
    expect(document.querySelectorAll("[data-property-row]")).toHaveLength(2);
    expect(requireElement("[data-property-toast]", HTMLElement).hidden).toBe(true);
  });
});

