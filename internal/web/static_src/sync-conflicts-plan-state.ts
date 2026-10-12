import type { StatusKind } from "./status";
import type { SyncOperationElements } from "./sync-conflict-render";

// PlanState is the receipt this page is holding and the three buttons that depend
// on it. It is one object because those two things must never disagree: an Apply
// button on screen with no receipt behind it, or a receipt still held after the
// decision changed, are the same bug seen from either side.
export type PlanState = {
  id(): string;
  planned(previewID: string): void;
  /** spend marks the receipt used, which a resolve request does whatever it answers. */
  spend(): void;
  /**
   * dropped un-plans, and says why unless the caller is saying something better.
   * A page holding no receipt has nothing to drop and says nothing.
   */
  dropped(sentence: string): void;
};

export const createPlanState = (
  elements: SyncOperationElements,
  say: (message: string, kind?: StatusKind) => void
): PlanState => {
  let previewID = "";
  const show = (planned: boolean): void => {
    if (elements.review) {
      elements.review.hidden = planned;
    }
    if (elements.apply) {
      elements.apply.hidden = !planned;
    }
    if (elements.cancel) {
      elements.cancel.hidden = !planned;
    }
  };
  // unplan takes the plan off the screen with the receipt it described. The two go
  // together in both directions a receipt can end: a plan still readable beside a
  // hidden Apply button is a page showing what it will do next to no way of doing
  // it, and "what this will do" is the sentence somebody would go back and re-read.
  const unplan = (): void => {
    previewID = "";
    elements.plan.hidden = true;
    elements.plan.textContent = "";
    show(false);
  };
  return {
    id: (): string => previewID,

    planned(id: string): void {
      previewID = id;
      show(true);
    },

    spend: unplan,

    dropped(sentence: string): void {
      // Nothing held is nothing to drop, and nothing to say either: a radio touched
      // before anything was reviewed would otherwise be answered with advice about
      // reviewing again, which is a step that has not happened yet.
      if (previewID === "") {
        return;
      }
      // The receipt is left to expire rather than spent at the server: the page has
      // nothing to redeem it for, and a resolve sent only to discard it is a write.
      unplan();
      if (sentence !== "") {
        say(sentence);
      }
    },
  };
};
