import { describe, expect, it } from "vitest";

import {
  decideDraftRecovery,
  describeDraftTime,
  draftOfferCopy,
  offerFromEditor,
  type DraftOffer,
} from "./source-draft-recovery";
import { draftStatusMessages } from "./source-draft-panel";
import { makeDraft as draft } from "./source-draft.test-helpers";

const loaded = (revision: string, content: string) => ({ kind: "loaded", revision, content }) as const;

describe("decideDraftRecovery against a vault that answered", () => {
  it("has nothing to offer when nothing was kept", () => {
    expect(decideDraftRecovery({ drafts: [], ownDraftID: "tab-1", vault: loaded("r1", "vault text\n") })).toEqual({
      offer: null,
      superseded: [],
    });
  });

  // The ordinary interruption: the tab went away, the note did not move, the
  // text comes back and the save that follows is an ordinary one.
  it("offers a draft written against the version the vault still holds as a resume", () => {
    const decision = decideDraftRecovery({
      drafts: [draft()],
      ownDraftID: "tab-1",
      vault: loaded("r1", "vault text\n"),
    });

    expect(decision.offer).toMatchObject({
      kind: "resume",
      sameTab: true,
      source: "storage",
      vaultRevision: "r1",
      vaultContent: "vault text\n",
    });
    expect(decision.superseded).toEqual([]);
  });

  // The note moved on — another device, the CLI, the other tab. The text is
  // still worth having and it is no longer based on what the vault holds, and
  // the offer has to say so rather than arm a save against a revision that is
  // gone.
  it("offers a draft written against an older version as stale", () => {
    const decision = decideDraftRecovery({
      drafts: [draft({ baseRevision: "r1" })],
      ownDraftID: "tab-1",
      vault: loaded("r9", "somebody else's text\n"),
    });

    expect(decision.offer).toMatchObject({ kind: "stale", vaultRevision: "r9", vaultContent: "somebody else's text\n" });
  });

  it("names a draft from another tab as such", () => {
    const decision = decideDraftRecovery({
      drafts: [draft({ draftID: "tab-2" })],
      ownDraftID: "tab-1",
      vault: loaded("r1", "vault text\n"),
    });

    expect(decision.offer).toMatchObject({ kind: "resume", sameTab: false });
  });

  // The save landed and the tab never learned it. Offering it back would be a
  // choice between two identical texts, and never clearing it would leave a
  // permanent panel over a note that is exactly as its author left it.
  it("supersedes a draft the vault already holds the text of", () => {
    const decision = decideDraftRecovery({
      drafts: [draft({ draftID: "tab-2", content: "vault text\n" })],
      ownDraftID: "tab-1",
      vault: loaded("r9", "vault text\n"),
    });

    expect(decision.offer).toBeNull();
    expect(decision.superseded).toEqual(["tab-2"]);
  });

  // Two tabs are two questions, asked in turn. One panel trying to be about both
  // would make the person answer for text they cannot see.
  it("offers only the newest survivor and leaves the rest in storage", () => {
    const decision = decideDraftRecovery({
      drafts: [
        draft({ draftID: "old", content: "older\n", updatedAt: 1_000 }),
        draft({ draftID: "new", content: "newer\n", updatedAt: 2_000 }),
      ],
      ownDraftID: "tab-1",
      vault: loaded("r1", "vault text\n"),
    });

    expect(decision.offer?.draft.draftID).toBe("new");
    expect(decision.superseded).toEqual([]);
  });
});

// The other half of the decision: no version to compare a draft against, for
// two reasons that look alike and are not. One is an answer — the note is not
// there — and the other is the absence of one.
describe("decideDraftRecovery with nothing to compare against", () => {
  // The note is gone: renamed, moved, or deleted. There is nothing to save over
  // and nothing to compare against.
  it("offers a draft for a note that is no longer there as missing", () => {
    const decision = decideDraftRecovery({
      drafts: [draft()],
      ownDraftID: "tab-1",
      vault: { kind: "missing" },
    });

    expect(decision.offer).toMatchObject({ kind: "missing", vaultRevision: "", vaultContent: "" });
    expect(decision.superseded).toEqual([]);
  });

  // Network loss, a stopped server, an Access session that expired. Nothing was
  // compared, so nothing is claimed and — the load-bearing half — nothing is
  // dropped: a vault nobody could reach supersedes no draft, however identical
  // it might have turned out to be.
  it("offers a draft as unreachable and supersedes nothing when the vault did not answer", () => {
    const decision = decideDraftRecovery({
      drafts: [draft({ draftID: "a", content: "same as the vault\n" }), draft({ draftID: "b", updatedAt: 2_000 })],
      ownDraftID: "tab-1",
      vault: { kind: "unreachable" },
    });

    expect(decision.offer).toMatchObject({ kind: "unreachable" });
    expect(decision.offer?.draft.draftID).toBe("b");
    expect(decision.superseded).toEqual([]);
  });
});

describe("offerFromEditor", () => {
  it("describes what the editor is holding rather than anything read back", () => {
    expect(offerFromEditor(draft())).toMatchObject({ kind: "missing", sameTab: true, source: "editor" });
  });
});

describe("draftOfferCopy", () => {
  const offer = (overrides: Partial<DraftOffer>): DraftOffer => ({
    kind: "resume",
    draft: draft(),
    sameTab: true,
    source: "storage",
    vaultContent: "",
    vaultRevision: "",
    ...overrides,
  });

  // The constraint the whole feature rests on. This text has never left the
  // browser: it is not on the Pi, not in the vault's history and not in any
  // backup. A person who reads "saved" here closes the tab.
  it("never says a word that could be read as a save", () => {
    // Both sources, because they have different opening sentences and the rule
    // is about every sentence this feature says. The editor-sourced one is the
    // save refusal's offer, and it is the one a person reads while looking at
    // text they have just been told could not be written.
    for (const source of ["storage", "editor"] as const) {
      for (const kind of ["resume", "stale", "missing", "unreachable"] as const) {
        const copy = draftOfferCopy(offer({ kind, source }), "moments ago");
        const prose = `${copy.heading} ${copy.lede}`;
        const where = `${kind}/${source}`;
        // Sentence by sentence, because "saved" is allowed here in exactly one
        // shape — a denial — and a whole-string match could not tell the two
        // apart.
        for (const sentence of prose.split(/(?<=\.)\s+/)) {
          if (/\bsaved\b/i.test(sentence)) {
            expect(sentence, where).toMatch(/\bnot saved\b/i);
          }
        }
        // Where the text is, said in whichever way that source can say it: one
        // names the browser it never left, the other says it was never written
        // anywhere at all.
        expect(prose.toLowerCase(), where).toMatch(
          source === "storage" ? /browser/ : /has not been written anywhere/
        );
      }
    }
  });

  // Every kind offers exactly the actions that kind has. A restore on a note
  // that is gone would save over nothing; a recreate on a note that is there
  // would write a second copy beside it.
  it("offers the actions the offer's kind actually has", () => {
    expect(draftOfferCopy(offer({ kind: "resume" }), "x")).toMatchObject({ restore: true, recreate: false });
    expect(draftOfferCopy(offer({ kind: "stale" }), "x")).toMatchObject({ restore: true, recreate: false });
    expect(draftOfferCopy(offer({ kind: "missing" }), "x")).toMatchObject({ restore: false, recreate: true });
    expect(draftOfferCopy(offer({ kind: "unreachable" }), "x")).toMatchObject({ restore: false, recreate: false });
  });

  it("says whose tab kept the text, and when", () => {
    expect(draftOfferCopy(offer({ sameTab: true }), "4 minutes ago").lede).toContain(
      "This tab kept this text in this browser 4 minutes ago"
    );
    expect(draftOfferCopy(offer({ sameTab: false }), "4 minutes ago").lede).toContain(
      "Another tab kept this text in this browser 4 minutes ago"
    );
    // An offer raised from the editor is about text on screen right now, so
    // there is no "kept ... ago" to claim.
    expect(draftOfferCopy(offer({ kind: "missing", source: "editor" }), "4 minutes ago").lede).toContain(
      "The text in the editor has not been written anywhere."
    );
  });

  it("falls back to a phrase when the draft names no path", () => {
    expect(draftOfferCopy(offer({ draft: draft({ relPath: "" }) }), "x").lede).toContain("this note has not changed");
  });
});

// The panel's rule, applied to the line underneath it and to every message it
// can hold rather than to the ones somebody remembered to test: these are what a
// person reads while deciding whether it is safe to close the tab, and a new
// message that said "saved" would otherwise reach a browser with nothing in its
// way.
describe("draftStatusMessages", () => {
  it("never says a word that could be read as a save", () => {
    for (const [name, message] of Object.entries(draftStatusMessages)) {
      for (const sentence of message.split(/(?<=\.)\s+/)) {
        if (/\bsaved\b/i.test(sentence)) {
          // The one sentence allowed to say it is the one reporting somebody
          // else's save, which is a thing that did reach the vault.
          expect(sentence, name).toMatch(/^Another tab saved this note\.$/);
        }
      }
    }
  });

  // The other half of the same rule, and the half avoiding one word does not
  // satisfy: a sentence saying text *is* somewhere has to say where. Left
  // unplaced, "your text is being kept" is read as the only other place this
  // application puts anything — the vault on the Pi — and saying nothing is how
  // a message clears the test above while still mislabeling local storage as a
  // save. The vocabulary rather than one phrasing, because the mistake is the
  // claim and not the wording of it.
  it("says where, whenever it says text is anywhere", () => {
    // The lead word is what separates a claim from a report of one ending: "the
    // kept text has been discarded" names no place and needs none. Every way of
    // saying the claim is spelled out here rather than one, because the next
    // message to be added will be phrased by somebody who has not read this.
    const custody =
      /\b(is|are|being|still|remains|remain|stays|stay|keeps|keep)\b[^.]*\b(kept|keeping|held|holding|stored|storing|safe|backed up)\b/i;
    const place = /\b(this browser|this tab|the editor)\b/i;
    for (const [name, message] of Object.entries(draftStatusMessages)) {
      for (const sentence of message.split(/(?<=\.)\s+/)) {
        if (custody.test(sentence)) {
          expect(sentence, name).toMatch(place);
        }
      }
    }
  });
});

describe("describeDraftTime", () => {
  const minute = 60_000;

  it("answers 'is this what I was just typing' without a locale in it", () => {
    expect(describeDraftTime(1_000, 1_000)).toBe("moments ago");
    expect(describeDraftTime(0, 44_000)).toBe("moments ago");
    expect(describeDraftTime(0, 60_000)).toBe("1 minute ago");
    expect(describeDraftTime(0, 4 * minute)).toBe("4 minutes ago");
    expect(describeDraftTime(0, 60 * minute)).toBe("1 hour ago");
    expect(describeDraftTime(0, 5 * 60 * minute)).toBe("5 hours ago");
    expect(describeDraftTime(0, 24 * 60 * minute)).toBe("1 day ago");
    expect(describeDraftTime(0, 3 * 24 * 60 * minute)).toBe("3 days ago");
  });

  // A clock that went backwards — a corrected system time, a draft written on
  // another device — must not read as "-3 minutes ago".
  it("never describes a draft as being from the future", () => {
    expect(describeDraftTime(10_000, 0)).toBe("moments ago");
  });
});
