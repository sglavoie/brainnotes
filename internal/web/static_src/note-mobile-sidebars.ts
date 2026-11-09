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

export const arrangeMobileCapture = (): void => {
  const launch = document.querySelector<HTMLElement>('.note-page [data-capture-launch]');
  const workspace = document.querySelector('.note-page .source-editor-workspace-content');
  if (!launch || !workspace) return;
  const marker = document.createComment('capture toolbar position');
  launch.before(marker);
  const mobile = window.matchMedia('(max-width: 700px)');
  const arrange = (): void => { if (mobile.matches) workspace.prepend(launch); else marker.after(launch); };
  mobile.addEventListener('change', arrange);
  arrange();
};
