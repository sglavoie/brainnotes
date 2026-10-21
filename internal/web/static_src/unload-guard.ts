// warnBeforeUnload asks the browser to confirm leaving the page while
// `unsaved` reports edits that exist only in this tab. The browser decides what
// to show; all a page can do is ask for the prompt, and both spellings are
// needed because browsers disagree about which one arms it. The returned
// function removes the listener.
export const warnBeforeUnload = (
  view: Window | null | undefined,
  unsaved: () => boolean
): (() => void) => {
  const onBeforeUnload = (event: BeforeUnloadEvent): void => {
    if (!unsaved()) {
      return;
    }
    event.preventDefault();
    event.returnValue = "";
  };
  view?.addEventListener("beforeunload", onBeforeUnload);
  return () => view?.removeEventListener("beforeunload", onBeforeUnload);
};
