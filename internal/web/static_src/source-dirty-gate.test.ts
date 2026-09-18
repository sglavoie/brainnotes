// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from "vitest";

import { createDirtyGate, gateMessage, type GuardedAction } from "./source-dirty-gate";

// The gate stands between unsaved text and an operation that would move it out of
// reach. Two refusals, not one: an operation that changes the note's *content*
// may be taken over a dirty editor, because the text is still on screen
// afterwards; one that changes its *identity* may not, because the kept draft is
// filed under a note key that would no longer exist.

const markup = `
<section data-source-gate hidden>
  <p data-source-gate-message></p>
  <button type="button" data-source-gate-save>Save first, then continue</button>
  <button type="button" data-source-gate-proceed hidden>Continue anyway</button>
  <button type="button" data-source-gate-cancel>Cancel</button>
</section>
`;

const read = () => ({
  panel: document.querySelector<HTMLElement>("[data-source-gate]"),
  message: document.querySelector<HTMLElement>("[data-source-gate-message]"),
  save: document.querySelector<HTMLButtonElement>("[data-source-gate-save]"),
  proceed: document.querySelector<HTMLButtonElement>("[data-source-gate-proceed]"),
  cancel: document.querySelector<HTMLButtonElement>("[data-source-gate-cancel]"),
});

type Harness = {
  gate: ReturnType<typeof createDirtyGate>;
  elements: ReturnType<typeof read>;
  ran: string[];
  statuses: { message: string; kind: string }[];
  saves: number;
  setDirty(value: boolean): void;
};

const harness = (options: { dirty: boolean; savesSucceed?: boolean }): Harness => {
  document.body.innerHTML = markup;
  const elements = read();
  const ran: string[] = [];
  const statuses: { message: string; kind: string }[] = [];
  let dirty = options.dirty;
  let saves = 0;

  const gate = createDirtyGate({
    elements,
    dirty: () => dirty,
    save: async () => {
      saves += 1;
      if (options.savesSucceed !== false) {
        dirty = false;
      }
    },
    setStatus: (message, kind) => statuses.push({ message, kind }),
  });

  return {
    gate,
    elements,
    ran,
    statuses,
    get saves(): number {
      return saves;
    },
    setDirty: (value: boolean) => {
      dirty = value;
    },
  };
};

const action = (over: Partial<GuardedAction> = {}, ran: string[] = []): GuardedAction => ({
  label: "Rename this note",
  allowDirty: false,
  run: async () => {
    ran.push("ran");
  },
  ...over,
});

beforeEach(() => {
  document.body.innerHTML = markup;
});

describe("createDirtyGate", () => {
  it("takes an action straight through when there is nothing unsaved", async () => {
    const h = harness({ dirty: false });
    h.gate.guard(action({}, h.ran));
    await vi.waitFor(() => expect(h.ran).toEqual(["ran"]));
    expect(h.elements.panel?.hidden).toBe(true);
    expect(h.gate.pending()).toBeNull();
  });

  it("holds an identity change and offers no way past it but saving", () => {
    const h = harness({ dirty: true });
    h.gate.guard(action({}, h.ran));

    expect(h.ran).toEqual([]);
    expect(h.elements.panel?.hidden).toBe(false);
    expect(h.elements.proceed?.hidden).toBe(true);
    expect(h.elements.message?.textContent).toContain("has not been done");
    expect(h.elements.save?.textContent).toBe("Save the note, then rename this note");
    expect(h.gate.pending()?.label).toBe("Rename this note");
  });

  it("offers Continue anyway for an action that only rewrites the note's content", () => {
    const h = harness({ dirty: true });
    h.gate.guard(action({ label: "Set this property", allowDirty: true }, h.ran));

    expect(h.elements.proceed?.hidden).toBe(false);
    expect(h.elements.proceed?.textContent).toBe("Set this property anyway");
  });

  it("saves first, then takes the action, and puts the panel away", async () => {
    const h = harness({ dirty: true });
    h.gate.guard(action({}, h.ran));
    h.elements.save?.click();

    await vi.waitFor(() => expect(h.ran).toEqual(["ran"]));
    expect(h.saves).toBe(1);
    expect(h.elements.panel?.hidden).toBe(true);
  });

  // Taking the action on the strength of having *tried* to save is how the text
  // this gate exists to protect gets left behind.
  it("does not take the action when the save was refused", async () => {
    const h = harness({ dirty: true, savesSucceed: false });
    h.gate.guard(action({}, h.ran));
    h.elements.save?.click();

    await vi.waitFor(() => expect(h.statuses).toHaveLength(1));
    expect(h.ran).toEqual([]);
    expect(h.statuses[0].kind).toBe("is-error");
    expect(h.statuses[0].message).toContain("rename this note has not been done");
  });

  it("proceeds over unsaved text only when the action said it may", async () => {
    const h = harness({ dirty: true });
    h.gate.guard(action({ label: "Set this property", allowDirty: true }, h.ran));
    h.elements.proceed?.click();
    await vi.waitFor(() => expect(h.ran).toEqual(["ran"]));
  });

  it("ignores a proceed on an action that refuses to run dirty", () => {
    const h = harness({ dirty: true });
    h.gate.guard(action({}, h.ran));
    h.elements.proceed?.click();
    expect(h.ran).toEqual([]);
    expect(h.elements.panel?.hidden).toBe(false);
  });

  it("says nothing was changed when the person backs out", () => {
    const h = harness({ dirty: true });
    h.gate.guard(action({}, h.ran));
    h.elements.cancel?.click();

    expect(h.ran).toEqual([]);
    expect(h.elements.panel?.hidden).toBe(true);
    expect(h.statuses).toEqual([
      { message: "Rename this note: canceled. Nothing was changed.", kind: "" },
    ]);
    expect(h.gate.pending()).toBeNull();
  });

  it("does nothing on a click after dispose", () => {
    const h = harness({ dirty: true });
    h.gate.guard(action({}, h.ran));
    h.gate.dispose();
    h.elements.cancel?.click();
    expect(h.elements.panel?.hidden).toBe(false);
  });

  it("survives a page that has no gate markup at all", async () => {
    const ran: string[] = [];
    const gate = createDirtyGate({
      elements: { panel: null, message: null, save: null, proceed: null, cancel: null },
      dirty: () => false,
      save: async () => {},
      setStatus: () => {},
    });
    gate.guard(action({}, ran));
    await vi.waitFor(() => expect(ran).toEqual(["ran"]));
  });
});

// The order of the sentence is the point: somebody who has just pressed a button
// they now suspect was destructive reads the first clause and stops worrying.
describe("gateMessage", () => {
  it("leads with what has not happened", () => {
    expect(gateMessage(action({ label: "Move this note to Trash" }))).toMatch(
      /^Move this note to Trash has not been done\./
    );
  });

  it("offers both ways out for a content change and only one for an identity change", () => {
    expect(gateMessage(action({ allowDirty: true }))).toContain("going ahead anyway");
    const identity = gateMessage(action({ allowDirty: false }));
    expect(identity).not.toContain("going ahead anyway");
    expect(identity).toContain("saved first, or not done");
  });
});
