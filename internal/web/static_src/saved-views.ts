import type { SavedViewSummary } from "./api-types";
import { queryElement } from "./dom";
import { browserQueryChangeEventName } from "./filters";
import { syncCurrentSavedView } from "./saved-views-current";
import { createSavedViewSaver, type SavedViewSaver } from "./saved-views-actions";
import {
  savedViewInputFromParams,
  savedViewsNavBasePath,
  type SavedViewCreateReceipt,
  type SavedViewPendingSave,
  type SavedViewsDeps,
} from "./saved-views-model";
import {
  renderSavedViewDiff,
  renderSavedViewReceipt,
  renderSavedViewsNav,
} from "./saved-views-render";
import { fetchSavedViews } from "./saved-views-requests";
import { bindSavedViewScope } from "./saved-views-scope";
import { setStatusText, type StatusKind } from "./status";

export type { SavedViewsDeps } from "./saved-views-model";

export type SavedViewsNavHandle = {
  render: (views: SavedViewSummary[]) => void;
};

/**
 * Hydrates the saved-views strip inside the primary nav.
 *
 * The nav is rendered client-side on purpose: `siteNav` is invoked from eight
 * templates and receives each page's own data dot, so threading a `SavedViews`
 * field through ten page-data structs would be a lot of churn for one strip.
 * One static container plus this fetch gets the same result uniformly on every
 * page, and app.css reserves the row height so hydration does not shift the
 * anchors beside it.
 */
export const initSavedViewsNav = (deps: SavedViewsDeps = {}): SavedViewsNavHandle | null => {
  const container = queryElement(document, "[data-saved-views-nav]", HTMLElement);
  if (!container) {
    return null;
  }
  const fetcher = deps.fetch || ((input, init) => fetch(input, init));
  const basePath = savedViewsNavBasePath(window.location.pathname);
  let currentViews: SavedViewSummary[] = [];
  const refreshCurrent = (): void => syncCurrentSavedView(container, currentViews);
  const render = (views: SavedViewSummary[]): void => {
    currentViews = views;
    renderSavedViewsNav(container, views, basePath);
    refreshCurrent();
  };
  window.addEventListener(browserQueryChangeEventName, refreshCurrent);
  window.addEventListener("popstate", refreshCurrent);

  void fetchSavedViews(fetcher).then((outcome) => {
    // A listing failure leaves the strip empty rather than reporting into the
    // nav: nothing else on the page depends on it, and an error row in the
    // primary navigation would be louder than the feature is important.
    if (outcome.ok) {
      render(outcome.result.views);
    }
  });

  return { render };
};

type SavedViewsSaveElements = {
  root: HTMLElement;
  openButton: HTMLButtonElement;
  form: HTMLFormElement;
  nameInput: HTMLInputElement;
  descriptionInput: HTMLInputElement | null;
  confirmButton: HTMLButtonElement | null;
  cancelButton: HTMLButtonElement | null;
  diffEl: HTMLElement | null;
  statusEl: HTMLElement | null;
  token: string;
};

const resolveSaveElements = (doc: Document): SavedViewsSaveElements | null => {
  const root = queryElement(doc, "[data-saved-views-save]", HTMLElement);
  if (!root) {
    return null;
  }
  const openButton = queryElement(root, "[data-saved-views-open]", HTMLButtonElement);
  const form = queryElement(root, "[data-saved-views-form]", HTMLFormElement);
  const nameInput = queryElement(root, "[data-saved-views-name]", HTMLInputElement);
  if (!openButton || !form || !nameInput) {
    return null;
  }
  return {
    root,
    openButton,
    form,
    nameInput,
    descriptionInput: queryElement(root, "[data-saved-views-description]", HTMLInputElement),
    confirmButton: queryElement(root, "[data-saved-views-confirm]", HTMLButtonElement),
    cancelButton: queryElement(root, "[data-saved-views-cancel]", HTMLButtonElement),
    diffEl: queryElement(root, "[data-saved-views-diff]", HTMLElement),
    statusEl: queryElement(root, "[data-saved-views-status]", HTMLElement),
    token: root.getAttribute("data-saved-views-csrf") || "",
  };
};

const bindSaveVisibility = (
  els: SavedViewsSaveElements, refreshScope: () => void, saver: SavedViewSaver
): void => {
  const setOpen = (open: boolean): void => {
    const wasOpen = !els.form.hidden;
    els.form.hidden = !open;
    els.openButton.setAttribute("aria-expanded", open ? "true" : "false");
    if (open) {
      refreshScope();
      els.nameInput.focus();
    } else if (wasOpen) {
      els.openButton.focus();
    }
  };

  els.openButton.addEventListener("click", () => {
    const open = Boolean(els.form.hidden);
    setOpen(open);
    if (!open) {
      saver.cancel();
    }
  });

  const cancel = (): void => {
    saver.cancel();
    setOpen(false);
  };
  els.cancelButton?.addEventListener("click", cancel);

  els.form.addEventListener("keydown", (event) => {
    if (event.key !== "Escape" || event.defaultPrevented || event.isComposing || els.form.hidden) return;
    event.preventDefault();
    event.stopPropagation();
    cancel();
  });

  setOpen(false);
};

/**
 * Wires the "Save as view" control in the Library header.
 *
 * Saves the URL's expression, ordering and cap. The scope summary explicitly
 * identifies text search and facet chips that are excluded from that shape.
 */
export const initSavedViewsSave = (
  deps: SavedViewsDeps = {},
  nav: SavedViewsNavHandle | null = null
): SavedViewSaver | null => {
  const els = resolveSaveElements(document);
  if (!els) {
    return null;
  }
  const refreshScope = bindSavedViewScope(els.form);
  const fetcher = deps.fetch || ((input, init) => fetch(input, init));

  const setStatus = (message: string, kind: StatusKind = ""): void => {
    setStatusText(els.statusEl, message, kind);
  };

  const showPending = (pending: SavedViewPendingSave | null): void => {
    renderSavedViewDiff(els.diffEl, pending);
    if (els.confirmButton) {
      els.confirmButton.hidden = pending === null;
    }
  };

  // The create receipt reuses the diff block but never the confirm button: the
  // write already happened and its preview was consumed, so the block is read
  // only.
  const showReceipt = (receipt: SavedViewCreateReceipt | null): void => {
    renderSavedViewReceipt(els.diffEl, receipt);
    if (els.confirmButton) {
      els.confirmButton.hidden = true;
    }
  };

  const saver = createSavedViewSaver({
    fetch: fetcher,
    token: els.token,
    hooks: {
      onStatus: setStatus,
      onPending: showPending,
      onReceipt: showReceipt,
      onSaved: (result) => {
        nav?.render(result.views);
        els.nameInput.value = "";
        if (els.descriptionInput) {
          els.descriptionInput.value = "";
        }
      },
    },
  });

  bindSaveVisibility(els, refreshScope, saver);

  els.form.addEventListener("submit", (event) => {
    event.preventDefault();
    refreshScope();
    void saver.save(
      savedViewInputFromParams(new URLSearchParams(window.location.search), {
        name: els.nameInput.value,
        description: els.descriptionInput ? els.descriptionInput.value : "",
      })
    );
  });

  els.confirmButton?.addEventListener("click", () => {
    void saver.confirm();
  });

  showPending(null);
  return saver;
};

/** The single symbol main.ts consumes for the saved-views cluster. */
export const initSavedViews = (deps: SavedViewsDeps = {}): void => {
  const nav = initSavedViewsNav(deps);
  initSavedViewsSave(deps, nav);
};
