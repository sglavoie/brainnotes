type Section = { link: HTMLAnchorElement; heading: HTMLElement };

const noteSections = (body: HTMLElement): Section[] => {
  const sections: Section[] = [];
  for (const link of document.querySelectorAll<HTMLAnchorElement>(".note-toc-list a")) {
    let id: string;
    try { id = decodeURIComponent(new URL(link.href).hash.slice(1)); }
    catch { continue; }
    const heading = document.getElementById(id);
    if (heading && body.contains(heading)) sections.push({ link, heading });
  }
  return sections;
};

export const trackNoteSection = (body: HTMLElement): (() => void) => {
  const sections = noteSections(body);
  if (!sections.length) return () => {};
  let frame = 0;
  const update = (): void => {
    frame = 0;
    let active = sections[0];
    for (const section of sections) {
      if (section.heading.getBoundingClientRect().top <= 96) active = section;
    }
    for (const section of sections) {
      if (section === active) section.link.setAttribute("aria-current", "location");
      else section.link.removeAttribute("aria-current");
    }
  };
  const schedule = (): void => {
    if (!frame) frame = window.requestAnimationFrame(update);
  };
  window.addEventListener("scroll", schedule, { passive: true });
  window.addEventListener("resize", schedule);
  window.addEventListener("hashchange", schedule);
  // Media can move headings after the initial layout.
  body.addEventListener("load", schedule, true);
  update();
  return () => {
    window.cancelAnimationFrame(frame);
    window.removeEventListener("scroll", schedule);
    window.removeEventListener("resize", schedule);
    window.removeEventListener("hashchange", schedule);
    body.removeEventListener("load", schedule, true);
  };
};

export const addCodeCopyControls = (body: HTMLElement): void => {
  for (const pre of body.querySelectorAll("pre")) {
    const code = pre.querySelector("code");
    if (!code || pre.parentElement?.hasAttribute("data-code-copy")) continue;
    const wrapper = document.createElement("div");
    wrapper.className = "note-code-block";
    wrapper.dataset.codeCopy = "";
    const controls = document.createElement("div");
    controls.className = "note-code-copy";
    controls.dataset.copyControls = "";
    const button = document.createElement("button");
    button.type = "button";
    button.className = "bn-button copy-button";
    button.textContent = "Copy code";
    button.dataset.copyText = code.textContent || "";
    button.dataset.copyLabel = "Code";
    const status = document.createElement("span");
    status.className = "copy-status";
    status.dataset.copyStatus = "";
    status.setAttribute("role", "status");
    status.setAttribute("aria-live", "polite");
    controls.append(button, status);
    pre.before(wrapper);
    wrapper.append(controls, pre);
  }
};

export const addSectionCopyControls = (body: HTMLElement): void => {
  for (const heading of body.querySelectorAll<HTMLElement>("h1[id], h2[id], h3[id], h4[id], h5[id], h6[id]")) {
    if (!heading.id || heading.parentElement?.classList.contains("note-section-heading")) continue;
    const url = new URL(window.location.href);
    url.search = "";
    url.hash = encodeURIComponent(heading.id);
    // Keep controls outside the heading so its accessible name and text stay
    // intact, including after a failed copy adds a manual-copy field.
    const controls = document.createElement("div");
    controls.className = "note-section-copy";
    controls.dataset.sectionCopy = "";
    controls.dataset.copyControls = "";
    const button = document.createElement("button");
    button.type = "button";
    button.className = "bn-button copy-button";
    button.textContent = "#";
    button.setAttribute("aria-label", "Copy section link");
    button.title = "Copy section link";
    button.setAttribute("aria-describedby", heading.id);
    button.dataset.copyText = url.href;
    button.dataset.copyLabel = "Section link";
    const status = document.createElement("span");
    status.className = "copy-status";
    status.dataset.copyStatus = "";
    status.setAttribute("role", "status");
    status.setAttribute("aria-live", "polite");
    controls.append(button, status);
    const wrapper = document.createElement("div");
    wrapper.className = "note-section-heading";
    heading.before(wrapper);
    wrapper.append(heading, controls);
  }
};

export const initNoteReading = (): void => {
  const body = document.querySelector<HTMLElement>("[data-note-body]");
  if (!body) return;
  addCodeCopyControls(body);
  addSectionCopyControls(body);
  trackNoteSection(body);
};
