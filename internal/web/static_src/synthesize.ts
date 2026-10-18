import { restoreSynthesisPosition } from "./synthesize-session";
import { queryElement } from "./dom";
import { bindSynthesisSelection, bindSynthesisDiscovery, bindSynthesisFields, bindSynthesisActions } from "./synthesize-bindings";
import { createSynthesisContextActions } from "./synthesize-context-actions";
import { createSynthesisCreateActions } from "./synthesize-create-actions";
import { createSynthesisDiscoveryActions } from "./synthesize-discovery";
import { createSynthesisRuntime } from "./synthesize-runtime";
import { createSynthesisSelectionActions } from "./synthesize-selection";

export type {
  SynthesisController,
  SynthesisDeps,
} from "./synthesize-runtime";

import type { SynthesisController, SynthesisDeps } from "./synthesize-runtime";

export const createSynthesisController = (
  root: HTMLElement,
  mount: HTMLElement,
  deps: SynthesisDeps = {}
): SynthesisController => {
  const restorePosition = restoreSynthesisPosition(root);
  const runtime = createSynthesisRuntime(root, mount, deps);
  const { model, view } = runtime;
  const selection = createSynthesisSelectionActions(runtime);
  const discovery = createSynthesisDiscoveryActions(runtime);
  const context = createSynthesisContextActions(runtime);
  const create = createSynthesisCreateActions(runtime);

  bindSynthesisSelection(runtime, mount, selection);
  bindSynthesisDiscovery(runtime, discovery);
  bindSynthesisFields(runtime);
  bindSynthesisActions(runtime, context, create);

  view.folderInput.value = model.folder;
  view.purposeSelect.value = model.purpose;
  runtime.render();

  const ready = (async (): Promise<void> => {
    await create.restore();
    runtime.offerRecovery(create.restore);
    await discovery.selectMode("balanced");
    restorePosition();
  })();
  return { ready };
};

export const initSynthesize = (deps: SynthesisDeps = {}): SynthesisController | null => {
  const root = queryElement(document, "[data-synthesize-root]", HTMLElement);
  if (!root) {
    return null;
  }
  const mount = queryElement(root, "[data-synthesize-workbench]", HTMLElement);
  if (!mount || root.getAttribute("data-synthesize-anchor-key") === "") {
    return null;
  }
  return createSynthesisController(root, mount, deps);
};
