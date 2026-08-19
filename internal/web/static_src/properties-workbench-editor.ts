export type WorkbenchEditor = {
  close: () => void;
  isOpen: () => boolean;
  mount: (
    parent: HTMLElement,
    title: string,
    submitLabel: string,
    build: (form: HTMLFormElement) => () => string | null
  ) => void;
  reset: () => void;
};

export const createWorkbenchEditor = (doc: Document): WorkbenchEditor => {
  let removeOpenEditor: (() => void) | null = null;

  const close = (): void => {
    const remove = removeOpenEditor;
    removeOpenEditor = null;
    remove?.();
  };

  const mount = (
    parent: HTMLElement,
    title: string,
    submitLabel: string,
    build: (form: HTMLFormElement) => () => string | null
  ): void => {
    close();
    const form = doc.createElement("form");
    form.className = "properties-editor";
    form.setAttribute("data-property-editor-form", "");

    const heading = doc.createElement("p");
    heading.className = "properties-editor-title";
    heading.textContent = title;
    form.appendChild(heading);

    const submit = build(form);

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
    confirm.className = "properties-button properties-button-primary";
    confirm.setAttribute("data-property-editor-submit", "");
    confirm.textContent = submitLabel;
    const cancel = doc.createElement("button");
    cancel.type = "button";
    cancel.className = "properties-button";
    cancel.setAttribute("data-property-editor-cancel", "");
    cancel.textContent = "Cancel";
    actions.append(confirm, cancel);
    form.appendChild(actions);

    form.addEventListener("submit", (event) => {
      event.preventDefault();
      const message = submit();
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

    parent.appendChild(form);
    removeOpenEditor = () => {
      form.remove();
    };
  };

  return {
    close,
    isOpen: () => removeOpenEditor !== null,
    mount,
    reset: () => {
      removeOpenEditor = null;
    },
  };
};
