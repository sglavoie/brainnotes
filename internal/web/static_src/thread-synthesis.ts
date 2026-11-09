import type { SynthesisRuntime } from "./synthesize-runtime";
import { addSynthesisSource, type SynthesisCandidate } from "./synthesize-sources";

export const initThreadSynthesis = (runtime: SynthesisRuntime, root: HTMLElement, hadSavedWork: boolean): void => {
  const buttons = [...root.querySelectorAll<HTMLButtonElement>("[data-thread-source-key]")];
  if (!buttons.length) return;
  const status = root.querySelector<HTMLElement>("[data-thread-synthesis-status]")!;
  const add = (button: HTMLButtonElement): void => {
    const candidate: SynthesisCandidate = { key: button.dataset.threadSourceKey!, title: button.dataset.threadSourceTitle!, route: button.dataset.threadSourcePath!, path: button.dataset.threadSourceKey!, detail: "Collected in this thread" };
    const result = addSynthesisSource(runtime.model, runtime.config, candidate);
    status.textContent = result.message;
    if (result.ok) { runtime.render(); runtime.renderCandidatePanels(); runtime.persist(); }
  };
  for (const button of buttons) button.addEventListener("click", () => add(button));
  if (!hadSavedWork && runtime.model.sources.length === 0) {
    if (buttons.length <= runtime.config.maxSupporters) {
      buttons.forEach(add);
      status.textContent = "Collected sources selected in thread order. Review them before preparing context.";
    } else {
      status.textContent = `This thread has ${buttons.length} sources. Choose up to ${runtime.config.maxSupporters} using the Add buttons.`;
    }
  }
};
