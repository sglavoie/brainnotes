// Do not publish intermediate IME text as a query. compositionend is followed
// by input in some browsers; ordinary debounce coalesces those two events.
export const bindSearchInput = (input: HTMLInputElement, search: () => void, pause: () => void): void => {
  let composing = false;
  input.addEventListener("compositionstart", () => { composing = true; pause(); });
  input.addEventListener("compositionend", () => { composing = false; search(); });
  input.addEventListener("input", (event) => {
    if (!composing && !(event instanceof InputEvent && event.isComposing)) search();
  });
};
