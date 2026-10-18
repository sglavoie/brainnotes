import { clearSynthesisState, readSynthesisState, synthesisStorageKey, type SynthesisStorageLike, type SynthesisStoredState } from "./synthesize-storage";
import { element } from "./synthesize-view-elements";

// Each document owns one slot. Reloads, duplicated tabs and independently opened
// workbenches can recover a copy, but never write into another document's slot.
export const synthesisRecoveryKeys = (storage: SynthesisStorageLike | null, vault: string, anchor: string, ownKey: string): string[] => {
  const legacy = synthesisStorageKey(vault, anchor);
  const scopedPrefix = `bn.synthesis-draft.${JSON.stringify([vault, anchor])}.`;
  try {
    const keys = Array.from({ length: storage?.length || 0 }, (_, i) => storage?.key?.(i))
      .filter((key): key is string => !!key && key.startsWith(scopedPrefix) && key !== ownKey);
    return [legacy, ...keys].filter((key) => readSynthesisState(storage, key) !== null);
  } catch { return []; }
};

type RecoveryOptions = {
  root: HTMLElement;
  storage: SynthesisStorageLike | null;
  keys: string[];
  restore: (state: SynthesisStoredState) => Promise<void>;
  confirm: (message: string) => boolean | Promise<boolean>;
};

export const mountSynthesisRecovery = (options: RecoveryOptions): void => {
  if (!options.keys.length) return;
  const doc = options.root.ownerDocument;
  const panel = element(doc, "details", "bn-disclosure synthesize-recovery", { "data-synthesize-recovery": "" });
  const summary = element(doc, "summary", "");
  summary.textContent = "Recover saved work";
  const explanation = element(doc, "p", "");
  explanation.textContent = "Each workbench keeps its own draft. Restore a copy here, or delete a saved copy you no longer need.";
  const status = element(doc, "p", "bn-status", { role: "status" });
  panel.append(summary, explanation, status);
  for (const key of options.keys) {
    const state = readSynthesisState(options.storage, key);
    if (state) panel.append(recoveryRow(options, key, state, status));
  }
  options.root.prepend(panel);
};

const recoveryRow = (options: RecoveryOptions, key: string, state: SynthesisStoredState, status: HTMLElement): HTMLElement => {
  const doc = options.root.ownerDocument;
  const row = element(doc, "div", "synthesize-recovery-row");
  const label = element(doc, "p", "");
  label.textContent = `${state.title || "Untitled draft"} — ${state.draft.slice(0, 100) || "Sources and research settings"}`;
  const restore = element(doc, "button", "bn-button");
  restore.type = "button";
  restore.textContent = "Restore copy";
  restore.addEventListener("click", async () => {
    if (!await options.confirm("Restore this saved work? This replaces the draft and selection on this page. Other saved copies are kept.")) return;
    restore.disabled = true;
    try {
      await options.restore(state);
      status.textContent = "Restored a copy. The original saved draft is still available.";
    } catch { status.textContent = "Could not restore the sources. Your saved copy is still available. Try again."; }
    finally { restore.disabled = false; }
  });
  const remove = element(doc, "button", "bn-button");
  remove.type = "button";
  remove.textContent = "Delete saved copy";
  remove.addEventListener("click", async () => {
    if (!await options.confirm("Delete this saved copy? This cannot be undone. Open workbenches keep their current text.")) return;
    if (!clearSynthesisState(options.storage, key)) {
      status.textContent = "This browser refused to delete the saved copy.";
      return;
    }
    row.remove();
    status.textContent = "Deleted the saved copy.";
    status.tabIndex = -1;
    status.focus();
  });
  row.append(label, restore, remove);
  return row;
};
