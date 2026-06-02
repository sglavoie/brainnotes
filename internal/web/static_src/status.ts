export type StatusKind = "" | "is-success" | "is-error" | "is-loading" | "is-empty";

export const statusClassNames = ["is-success", "is-error", "is-loading", "is-empty"] as const;

export const setStatusText = (
  element: Element | null | undefined,
  message: string,
  kind: StatusKind = "",
  classNames: readonly string[] = statusClassNames
): void => {
  if (!element) {
    return;
  }
  element.textContent = message;
  element.classList.remove(...classNames);
  if (kind) {
    element.classList.add(kind);
  }
};
