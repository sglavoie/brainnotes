export type StatusKind = "" | "is-success" | "is-error" | "is-loading" | "is-empty" | "is-warning";

export const statusClassNames = [
  "is-success",
  "is-error",
  "is-loading",
  "is-empty",
  "is-warning",
] as const;

export const setStatusText = (
  element: Element | null | undefined,
  message: string,
  kind: StatusKind = "",
  classNames: readonly string[] = statusClassNames
): void => {
  if (!element) {
    return;
  }
  element.classList.add("bn-status");
  element.textContent = message;
  element.classList.remove(...classNames);
  if (kind) {
    element.classList.add(kind);
  }
};
