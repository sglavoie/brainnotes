export const initNoteMobileSidebars = (): void => {
  const sidebars = document.querySelectorAll<HTMLDetailsElement>("[data-note-sidebar-details]");
  if (!sidebars.length) return;
  const mobile = window.matchMedia("(max-width: 960px)");
  for (const sidebar of sidebars) {
    let mobileOpen = false;
    sidebar.open = !mobile.matches;
    mobile.addEventListener("change", () => {
      if (mobile.matches) {
        sidebar.open = mobileOpen;
      } else {
        mobileOpen = sidebar.open;
        sidebar.open = true;
      }
    });
  }
};
