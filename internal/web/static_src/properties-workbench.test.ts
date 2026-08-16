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

const waitForExpect = async (assertion: () => void): Promise<void> => {
  let lastError: unknown;
  for (let attempt = 0; attempt < 30; attempt += 1) {
    try {
      assertion();
      return;
    } catch (error) {
      lastError = error;
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
  }
  throw lastError;
};

const jsonResponse = (body: unknown, status = 200): Response =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

const summaryFixture = (overrides: Partial<PropertySummary> = {}): PropertySummary => ({
  name: "status",
  path: "/properties/status",
  configured: true,
  definition: {
    type: "string",
    items: "",
    required: true,
    allowed: ["active", "draft"],
    summary: "type: string, required, allowed: active, draft",
  },
  note_count: 2,
  total_notes: 3,
  coverage: 0.66,
  missing_required: 1,
  observed_kinds: ["string"],
  distinct_values: 2,
  violation_notes: 1,
  violations: 1,
  violation_counts: { missing: 1, type: 0, value: 0 },
  unsupported_values: 0,
  unsupported_notes: 0,
  issues: 1,
  ...overrides,
});

const mutationFixture = (
  overrides: Partial<PropertyMutationResult> = {}
): PropertyMutationResult => ({
  ok: true,
  generation: 3,
  action: "unset",
  property: "status",
  new_name: "",
  selected: 1,
  affected: 1,
  previews: [
    {
      rel_path: "Notes/Alpha.md",
      changes: [
        {
          kind: "frontmatter",
          key: "status",
          old: { present: true, text: "active" },
          new: { present: false, text: "" },
        },
      ],
    },
  ],
  blockers: [],
  config_diff: [],
  projected_violations: {
    before: { missing: 1, type: 0, value: 0, notes: 1 },
    after: { missing: 2, type: 0, value: 0, notes: 2 },
  },
  preview_id: "receipt-1",
  expires_at: new Date(Date.now() + 300000).toISOString(),
  journal_id: "",
  config: {
    generation: 3,
    source_hash: "abc",
    status: "ok",
    restart_required: [],
    writable: true,
  },
  ...overrides,
});

const detailPayload = (overrides: Record<string, unknown> = {}) => ({
  ok: true,
  generation: 3,
  property: summaryFixture(),
  values: [
    { value: { kind: "string", text: "active", value: "active", supported: true }, notes: 1 },
    { value: { kind: "string", text: "draft", value: "draft", supported: true }, notes: 1 },
  ],
  rows: [
    {
      rel_path: "Notes/Alpha.md",
      title: "Alpha",
      folder: "Notes",
      path: "/note/notes/alpha",
      hash: "hash-alpha",
      present: true,
      value: { kind: "string", text: "active", value: "active", supported: true },
      issues: [],
      unsupported: false,
    },
    {
      rel_path: "Notes/Beta.md",
      title: "Beta",
      folder: "Notes",
      path: "/note/notes/beta",
      hash: "hash-beta",
      present: true,
      value: { kind: "string", text: "draft", value: "draft", supported: true },
      issues: [],
      unsupported: false,
    },
  ],
  pagination: { limit: 50, offset: 0, total: 2, returned: 2, has_more: false },
  filter: { status: "all" },
  config: {
    generation: 3,
    source_hash: "abc",
    status: "ok",
    restart_required: [],
    writable: true,
  },
  ...overrides,
});

const mountWorkbench = (token = "tok"): HTMLElement => {
  document.body.innerHTML = `
    <section data-property-detail
      data-property-name="status"
      data-property-csrf="${token}"
      data-property-history-path="#property-history"
      data-properties-detail-endpoint="/api/properties/detail">
      <p data-property-status></p>
      <p data-property-config hidden></p>
      <p data-property-config-invalid hidden></p>
      <p data-property-config-restart hidden></p>
      <p data-property-definition hidden></p>
      <dl data-property-summary></dl>
      <div data-property-proposal hidden></div>
      <section data-property-schema>
        <select data-property-schema-type></select>
        <div data-property-schema-items-row hidden>
          <select data-property-schema-items></select>
        </div>
        <input type="checkbox" data-property-schema-required>
        <textarea data-property-schema-allowed></textarea>
        <p data-property-schema-error hidden></p>
        <button type="button" data-property-schema-save>Save definition</button>
        <button type="button" data-property-schema-delete disabled>Remove definition</button>
        <button type="button" data-property-schema-adopt hidden>Use inferred definition</button>
      </section>
      <ul data-property-values></ul>
      <select data-property-filter-status>
        <option value="all">All</option>
        <option value="violations">Violations</option>
        <option value="missing">Missing</option>
        <option value="unsupported">Unsupported</option>
      </select>
      <input data-property-filter-query>
      <button type="button" data-property-filter-clear hidden>Clear</button>
      <p data-property-active-value hidden></p>
      <div>
        <p data-property-selection-count>No notes selected.</p>
        <button type="button" data-property-select-all>Select page</button>
        <button type="button" data-property-clear-selection disabled>Clear selection</button>
        <button type="button" data-property-action="set" disabled>Set value</button>
        <button type="button" data-property-action="replace_value" disabled>Replace value</button>
        <button type="button" data-property-action="unset" disabled>Remove property</button>
        <button type="button" data-property-action="rename_property" disabled>Rename property</button>
        <button type="button" data-property-action="repair" disabled>Repair</button>
      </div>
      <div data-property-editor></div>
      <ul data-property-rows></ul>
      <button type="button" data-property-prev disabled>Previous</button>
      <p data-property-pagination></p>
      <button type="button" data-property-next disabled>Next</button>
      <div data-property-dialog-host></div>
      <div data-property-toast-host></div>
    </section>
  `;
  const root = document.querySelector("[data-property-detail]");
  if (!(root instanceof HTMLElement)) {
    throw new Error("detail root missing");
  }
  return root;
};

type Route = (body: unknown) => unknown;

const routedFetch = (routes: Record<string, Route>) =>
  vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const key = Object.keys(routes).find((candidate) => url.startsWith(candidate));
    if (!key) {
      return Promise.resolve(jsonResponse({ ok: false, message: "no route" }, 404));
    }
    const raw = typeof init?.body === "string" ? init.body : "";
    let parsed: unknown = raw;
    try {
      parsed = raw === "" ? undefined : JSON.parse(raw);
    } catch {
      parsed = raw;
    }
    const route = routes[key];
    if (!route) {
      return Promise.resolve(jsonResponse({ ok: false, message: "no route" }, 404));
    }
    return Promise.resolve(jsonResponse(route(parsed)));
  });

const requireElement = <T extends Element>(
  selector: string,
  ctor: new () => T
): T => {
  const found = document.querySelector(selector);
  if (!(found instanceof ctor)) {
    throw new Error(`missing ${selector}`);
  }
  return found;
};

const submitEditor = (): void => {
  const form = requireElement("[data-property-editor-form]", HTMLFormElement);
  form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
};

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
