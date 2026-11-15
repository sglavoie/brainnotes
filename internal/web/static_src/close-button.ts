export const closeButton = (name: string, close: () => void): HTMLButtonElement => {
  const button = document.createElement("button");
  button.type = "button";
  button.className = "bn-button bn-quiet bn-close";
  button.setAttribute("aria-label", name);
  button.title = name;
  button.innerHTML = '<svg viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" stroke-width="1.7" aria-hidden="true"><path d="m6 6 12 12M18 6 6 18"/></svg>';
  button.addEventListener("click", close);
  return button;
};
