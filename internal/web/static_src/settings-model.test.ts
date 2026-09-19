import { describe, expect, it } from "vitest";

import type { SettingSummary, SettingValue } from "./api";
import {
  formatSettingValue,
  parseSettingInput,
  settingBlockedReason,
  settingConfirmMessage,
  settingEditorText,
  settingOriginLabel,
  settingPendingSave,
  settingSavedMessage,
  settingValueOf,
  type SettingPendingSave,
} from "./settings-model";
import {
  newTemplateDraft,
  suggestTemplateName,
  templateConfirmMessage,
  templateDirNotice,
  templateDraftOf,
  templateSaveError,
  templateSavedMessage,
} from "./templates-model";

const summary = (overrides: Partial<SettingSummary> = {}): SettingSummary => ({
  key: "capture.file",
  kind: "string",
  summary: "Note quick captures are appended to.",
  assigned: true,
  layer: "portable",
  shadowed: false,
  value: settingValueOf("string", { text: "Inbox.md" }),
  ...overrides,
});

describe("setting values", () => {
  it("renders an unassigned setting as the default it resolves to", () => {
    // Blank would be a third state the value does not have: "the file does not
    // say" is carried by `assigned`, never by the rendered value.
    expect(formatSettingValue(settingValueOf("string", { text: "Inbox.md" }))).toBe("Inbox.md");
    expect(formatSettingValue(settingValueOf("string", {}))).toBe("(empty)");
    expect(formatSettingValue(settingValueOf("bool", { flag: true }))).toBe("true");
    expect(formatSettingValue(settingValueOf("int", { number: 25 }))).toBe("25");
    expect(formatSettingValue(settingValueOf("string_list", { items: ["a", "b"] }))).toBe("a, b");
    expect(formatSettingValue(settingValueOf("string_list", {}))).toBe("(empty list)");
  });

  it("seeds a list control with one item per line", () => {
    expect(settingEditorText(settingValueOf("string_list", { items: ["drafts", "archive"] }))).toBe(
      "drafts\narchive"
    );
    expect(settingEditorText(settingValueOf("int", { number: 7 }))).toBe("7");
    expect(settingEditorText(settingValueOf("bool", { flag: false }))).toBe("false");
  });

  it("carries the declared kind on every value it builds", () => {
    // The kind travels so the server can refuse a value that disagrees with the
    // key rather than guessing which field was meant.
    expect(settingValueOf("int", { number: 3 })).toEqual({
      kind: "int",
      text: "",
      number: 3,
      flag: false,
      items: [],
    });
  });

  it("copies the list it is given rather than aliasing it", () => {
    const items = ["one"];
    const value = settingValueOf("string_list", { items });
    items.push("two");
    expect(value.items).toEqual(["one"]);
  });
});

describe("parseSettingInput", () => {
  it("reads a bool from the checkbox's two answers only", () => {
    expect(parseSettingInput("bool", "true")).toEqual({
      ok: true,
      value: settingValueOf("bool", { flag: true }),
    });
    expect(parseSettingInput("bool", "false")).toEqual({
      ok: true,
      value: settingValueOf("bool", { flag: false }),
    });
  });

  it("refuses anything that is not a whole number", () => {
    for (const raw of ["", "  ", "12.5", "twelve", "1e3", "0x10"]) {
      expect(parseSettingInput("int", raw)).toEqual({
        ok: false,
        message: "This setting takes a whole number.",
      });
    }
    expect(parseSettingInput("int", " -4 ")).toEqual({
      ok: true,
      value: settingValueOf("int", { number: -4 }),
    });
  });

  it("refuses a number too large to survive the round trip", () => {
    expect(parseSettingInput("int", "99999999999999999999")).toEqual({
      ok: false,
      message: "That number is too large to store.",
    });
  });

  it("splits a list on newlines and drops the blank ones", () => {
    expect(parseSettingInput("string_list", "drafts\n\n  archive  \n")).toEqual({
      ok: true,
      value: settingValueOf("string_list", { items: ["drafts", "archive"] }),
    });
  });

  it("accepts a value the server is going to refuse", () => {
    // This is the client's only type judgement and it is deliberately narrow: a
    // heading with no leading `#` *is* a string, and the loader is the thing that
    // knows `capture.heading` must be an ATX heading. Holding a second copy of
    // that rule here is what would let the browser disagree with the vault.
    expect(parseSettingInput("string", "Captures")).toEqual({
      ok: true,
      value: settingValueOf("string", { text: "Captures" }),
    });
  });
});

describe("setting origin and refusals", () => {
  it("names the file that assigned the key, or the default", () => {
    expect(settingOriginLabel(summary())).toBe("brainnotes.toml");
    expect(settingOriginLabel(summary({ layer: "local" }))).toBe("brainnotes.local.toml");
    expect(settingOriginLabel(summary({ assigned: false }))).toBe("default");
  });

  it("explains a shadowed key in advance rather than after the typing", () => {
    expect(settingBlockedReason(summary())).toBe("");
    const blocked = settingBlockedReason(summary({ shadowed: true }));
    expect(blocked).toContain("brainnotes.local.toml");
    expect(blocked).toContain("capture.file");
  });
});

describe("setting receipts", () => {
  it("copies the diff onto the pending save", () => {
    const diff = [{ kind: "+" as const, line: 4, text: 'file = "Inbox.md"' }];
    const pending = settingPendingSave("capture.file", "set", null, {
      preview_id: "receipt-1",
      config_diff: diff,
      settings: [],
    });
    diff.push({ kind: "+" as const, line: 5, text: "stray" });
    expect(pending).toEqual({
      key: "capture.file",
      op: "set",
      previewID: "receipt-1",
      diff: [{ kind: "+", line: 4, text: 'file = "Inbox.md"' }],
      effective: "",
    });
  });

  it("says which file the write touches, in both directions", () => {
    const empty = { preview_id: "r", config_diff: [], settings: [] };
    const set = settingPendingSave("capture.file", "set", null, empty);
    const unset = settingPendingSave("capture.file", "unset", null, empty);
    expect(settingConfirmMessage(set)).toContain("Write");
    expect(settingConfirmMessage(set)).toContain("brainnotes.toml");
    expect(settingConfirmMessage(unset)).toContain("Remove");
    expect(settingSavedMessage(unset)).toContain("back to its default");
  });

  // The loader's silent normalizations are the reason this exists: a value it
  // replaces produces an accurate diff and a successful write, and the vault
  // still never adopts what was typed. The confirmation is the last place that
  // can be said before it happens.
  const projected = (
    key: string,
    requested: SettingValue,
    stored: SettingValue
  ): SettingPendingSave =>
    settingPendingSave(key, "set", requested, {
      preview_id: "r",
      config_diff: [],
      settings: [summary({ key, value: stored })],
    });

  it("names the stored value when it is not the one that was typed", () => {
    // An absolute templates.dir is replaced by the default, silently.
    const replaced = projected(
      "templates.dir",
      settingValueOf("string", { text: "/Users/me/Templates" }),
      settingValueOf("string", { text: "Templates" })
    );
    expect(replaced.effective).toBe("Templates");
    expect(settingConfirmMessage(replaced)).toContain("will store it as Templates");

    // A relative one is kept, and a confirmation that named it anyway would be
    // describing a substitution that did not happen.
    const kept = projected(
      "templates.dir",
      settingValueOf("string", { text: "Skeletons" }),
      settingValueOf("string", { text: "Skeletons" })
    );
    expect(kept.effective).toBe("");
    expect(settingConfirmMessage(kept)).not.toContain("will store it as");
  });

  it("names it for every kind the loader rewrites, not only for text", () => {
    // The other two silent rules: a zero scalar takes its default, and a list is
    // merged with the entries the vault always ignores.
    const zeroed = projected(
      "cockpit.daily_limit",
      settingValueOf("int", { number: 0 }),
      settingValueOf("int", { number: 5 })
    );
    expect(settingConfirmMessage(zeroed)).toContain("will store it as 5");

    const merged = projected(
      "ignore",
      settingValueOf("string_list", { items: ["drafts"] }),
      settingValueOf("string_list", { items: [".git", "drafts"] })
    );
    expect(settingConfirmMessage(merged)).toContain("will store it as .git, drafts");
  });

  it("says nothing about a stored value an unset never asked for", () => {
    // An unset asks for whatever the default resolves to, so the value the vault
    // ends up holding is by definition the one that was requested.
    const removed = settingPendingSave("templates.dir", "unset", null, {
      preview_id: "r",
      config_diff: [],
      settings: [summary({ key: "templates.dir", value: settingValueOf("string", { text: "Templates" }) })],
    });
    expect(removed.effective).toBe("");
    expect(settingConfirmMessage(removed)).not.toContain("will store it as");
  });
});

describe("template drafts", () => {
  it("opens a draft on the bytes and revision it was read at", () => {
    const draft = templateDraftOf({
      name: "Daily.md",
      rel_path: "Templates/Daily.md",
      size: 12,
      revision: "sha256:abc",
      content: "# {{ .Title }}\n",
    });
    expect(draft).toEqual({
      name: "Daily.md",
      content: "# {{ .Title }}\n",
      revision: "sha256:abc",
      created: false,
    });
    // A file that does not exist yet says so with an empty revision, which is
    // what the server reads as "create" and what disables the delete button.
    expect(newTemplateDraft("Weekly.md", "").revision).toBe("");
    expect(newTemplateDraft("Weekly.md", "").created).toBe(true);
  });

  it("refuses only what no server could accept", () => {
    // The name policy depends on the configured extension and on what the
    // directory holds, so it stays the server's; restating it here is how the
    // browser would end up disagreeing with the vault about a legal name.
    expect(templateSaveError({ ...templateDraftOf(emptySummary()), name: "  " })).toBe(
      "Name the template before saving it."
    );
    expect(templateSaveError({ ...templateDraftOf(emptySummary()), name: "Daily.md" })).toBe("");
    expect(templateSaveError({ ...templateDraftOf(emptySummary()), name: "../escape" })).toBe("");
  });

  it("appends the configured extension to a bare stem and nothing else", () => {
    expect(suggestTemplateName(" daily ", ".md")).toBe("daily.md");
    expect(suggestTemplateName("daily.md", ".markdown")).toBe("daily.md");
    expect(suggestTemplateName("daily", "")).toBe("daily");
    expect(suggestTemplateName("  ", ".md")).toBe("");
  });
});

const emptySummary = () => ({
  name: "Daily.md",
  rel_path: "Templates/Daily.md",
  size: 0,
  revision: "",
  content: "",
});

describe("template messages", () => {
  const pending = (op: "save" | "delete", created: boolean) => ({
    name: "Daily.md",
    op,
    content: "# {{ .Title }}\n",
    previewID: "r",
    diff: [],
    created,
  });

  it("distinguishes a create from an overwrite and a delete", () => {
    expect(templateConfirmMessage(pending("save", true))).toContain("Create");
    expect(templateConfirmMessage(pending("save", false))).toContain("Overwrite");
    expect(templateConfirmMessage(pending("delete", false))).toContain("Delete");
    expect(templateSavedMessage(pending("save", true))).toBe('Created "Daily.md".');
    expect(templateSavedMessage(pending("save", false))).toBe('Saved "Daily.md".');
    expect(templateSavedMessage(pending("delete", false))).toContain("Undo it from Recent changes");
  });
});

describe("templateDirNotice", () => {
  it("states both properties the directory has to have, in all four combinations", () => {
    const notice = (excluded: boolean, portable: boolean) =>
      templateDirNotice("Templates", { excluded_from_notes: excluded, portable });

    expect(notice(true, true)).toBe(
      "Templates/ is excluded from note queries and synchronizes with your other vaults."
    );
    expect(notice(true, false)).toBe(
      "Templates/ is excluded from note queries but does not synchronize."
    );
    expect(notice(false, true)).toBe(
      "Templates/ synchronizes but is indexed as ordinary notes."
    );
    expect(notice(false, false)).toBe(
      "Templates/ is indexed as ordinary notes and does not synchronize."
    );
  });

  it("names the directory generically when the server did not name one", () => {
    expect(templateDirNotice("", { excluded_from_notes: true, portable: true })).toContain(
      "The templates directory"
    );
  });
});
