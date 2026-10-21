// EditorForm is what a form builder hands back: how to submit it, and which
// control should take focus once the form is in the document. Focus has to
// wait for mounting, because focus() on a detached element does nothing.
export type EditorForm = {
  submit: () => string | null;
  focus: () => void;
};

export type WorkbenchEditor = {
  close: () => void;
  isOpen: () => boolean;
  mount: (
    parent: HTMLElement,
    title: string,
    submitLabel: string,
    build: (form: HTMLFormElement) => EditorForm
  ) => void;
  // refocus returns focus to the opener, or the control that replaced it,
  // when a repaint of the rows dropped focus onto <body>.
  refocus: () => void;
  reset: () => void;
};

// The attributes that identify an opener, so a repainted page can find the
// control that replaced it.
const openerAttributes = [
  "data-property-row-edit",
  "data-property-row-add",
  "data-property-action",
  "data-property-row",
];

type Opener = { element: HTMLElement; name: string; value: string; row: string };

const focusable = (element: Element | null): element is HTMLElement =>
  element instanceof HTMLElement &&
  element.isConnected &&
  !(element instanceof HTMLButtonElement && element.disabled);

const findByAttribute = (doc: Document, name: string, value: string): HTMLElement | null => {
  if (name === "" || value === "") {
    return null;
  }
  for (const candidate of Array.from(doc.querySelectorAll(`[${name}]`))) {
    if (candidate.getAttribute(name) === value && focusable(candidate)) {
      return candidate;
    }
  }
  return null;
};

const describeOpener = (element: HTMLElement): Opener => {
  const name = openerAttributes.find((candidate) => element.hasAttribute(candidate)) || "";
  return {
    element,
    name,
    value: name ? element.getAttribute(name) || "" : "",
    row: element.closest("[data-property-row]")?.getAttribute("data-property-row") || "",
  };
};

// focusTarget is the opener itself while it can take focus, otherwise its
// replacement after a repaint, otherwise its row.
const focusTarget = (doc: Document, opener: Opener): HTMLElement | null =>
  focusable(opener.element)
    ? opener.element
    : findByAttribute(doc, opener.name, opener.value) ||
      findByAttribute(doc, "data-property-row", opener.row);

// buildForm assembles the editor: the title, the builder's controls, the
// error line and the Save/Cancel actions.
const buildForm = (
  doc: Document,
  title: string,
  submitLabel: string,
  build: (form: HTMLFormElement) => EditorForm,
  close: () => void
): { form: HTMLFormElement; built: EditorForm } => {
  const form = doc.createElement("form");
  form.className = "properties-editor";
  form.setAttribute("data-property-editor-form", "");

  const heading = doc.createElement("p");
  heading.className = "properties-editor-title";
  heading.textContent = title;
  form.appendChild(heading);

  const built = build(form);

  const error = doc.createElement("p");
  error.className = "properties-editor-error";
  error.setAttribute("data-property-editor-error", "");
  error.setAttribute("role", "alert");
  error.hidden = true;
  form.appendChild(error);

  const actions = doc.createElement("div");
  actions.className = "properties-editor-actions";
  const confirm = doc.createElement("button");
  confirm.type = "submit";
  confirm.className = "bn-button properties-button properties-button-primary";
  confirm.setAttribute("data-property-editor-submit", "");
  confirm.textContent = submitLabel;
  const cancel = doc.createElement("button");
  cancel.type = "button";
  cancel.className = "bn-button properties-button";
  cancel.setAttribute("data-property-editor-cancel", "");
  cancel.textContent = "Cancel";
  actions.append(confirm, cancel);
  form.appendChild(actions);

  form.addEventListener("submit", (event) => {
    event.preventDefault();
    const message = built.submit();
    error.textContent = message || "";
    error.hidden = message === null || message === "";
  });
  form.addEventListener("keydown", (event) => {
    if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      close();
    }
  });
  cancel.addEventListener("click", close);
  return { form, built };
};

export const createWorkbenchEditor = (doc: Document): WorkbenchEditor => {
  let removeOpenEditor: (() => void) | null = null;
  let openForm: HTMLFormElement | null = null;
  let opener: Opener | null = null;

  const focusLost = (): boolean => {
    const active = doc.activeElement;
    return !active || active === doc.body || !active.isConnected;
  };

  const restore = (): void => {
    if (opener && focusLost()) {
      focusTarget(doc, opener)?.focus();
    }
  };

  const close = (): void => {
    const remove = removeOpenEditor;
    removeOpenEditor = null;
    openForm = null;
    remove?.();
    if (remove) {
      restore();
    }
  };

  const mount = (
    parent: HTMLElement,
    title: string,
    submitLabel: string,
    build: (form: HTMLFormElement) => EditorForm
  ): void => {
    const active = doc.activeElement;
    // Opening from inside the open form keeps the original opener, since the
    // control that has focus is removed along with that form.
    const fromOpenForm = openForm !== null && active !== null && openForm.contains(active);
    removeOpenEditor?.();
    removeOpenEditor = null;
    if (!fromOpenForm) {
      opener = active instanceof HTMLElement && active !== doc.body ? describeOpener(active) : null;
    }
    const { form, built } = buildForm(doc, title, submitLabel, build, close);

    parent.appendChild(form);
    openForm = form;
    removeOpenEditor = () => {
      form.remove();
    };
    built.focus();
  };

  return {
    close,
    isOpen: () => removeOpenEditor !== null,
    mount,
    refocus: () => {
      restore();
      opener = null;
    },
    reset: () => {
      removeOpenEditor = null;
      openForm = null;
    },
  };
};
