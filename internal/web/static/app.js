(function () {
  const notePathForKey = (key) => {
    if (!key) {
      return "";
    }
    const segments = String(key).split("/");
    const escaped = segments.map((segment) => encodeURIComponent(segment));
    return `/n/${escaped.join("/")}`;
  };

  const sharedFilterParamKeys = [
    "tag",
    "category",
    "status",
    "type",
    "folder",
    "has",
    "missing",
    "orphan",
    "broken",
    "dead_end",
    "age",
    "created",
    "min_backlinks",
    "max_backlinks",
    "min_links",
    "max_links",
  ];
  const repeatedFilterParams = new Set(["tag", "category"]);

  const facetGroups = [
    { key: "tags", label: "Tags", param: "tag", repeat: true },
    { key: "categories", label: "Categories", param: "category", repeat: true },
    { key: "status", label: "Status", param: "status" },
    { key: "types", label: "Types", param: "type" },
    { key: "folders", label: "Folders", param: "folder" },
    { key: "properties", label: "Properties", param: "has" },
    { key: "issues", label: "Issues", issue: true },
  ];

  const normalizeParamValue = (value) => String(value || "").trim();

  const readPageQuery = () => {
    const params = new URLSearchParams(window.location.search);
    return normalizeParamValue(params.get("q"));
  };

  const hasActiveSharedFilters = (params) => {
    const source = params || new URLSearchParams(window.location.search);
    return sharedFilterParamKeys.some((key) =>
      source.getAll(key).some((value) => normalizeParamValue(value) !== "")
    );
  };

  const copySharedFilterParams = (fromParams, toParams) => {
    for (const key of sharedFilterParamKeys) {
      for (const value of fromParams.getAll(key)) {
        const normalized = normalizeParamValue(value);
        if (normalized !== "") {
          toParams.append(key, normalized);
        }
      }
    }
  };

  const updateBrowserQuery = (params) => {
    if (!window.history || typeof window.history.replaceState !== "function") {
      return;
    }
    const query = params.toString();
    const nextURL = `${window.location.pathname}${query ? `?${query}` : ""}${window.location.hash}`;
    const currentURL = `${window.location.pathname}${window.location.search}${window.location.hash}`;
    if (nextURL !== currentURL) {
      window.history.replaceState(null, "", nextURL);
    }
  };

  const syncPageQueryParam = (query) => {
    const params = new URLSearchParams(window.location.search);
    const normalized = normalizeParamValue(query);
    if (normalized === "") {
      params.delete("q");
    } else {
      params.set("q", normalized);
    }
    updateBrowserQuery(params);
  };

  const buildFilteredAPIURL = (path, query) => {
    const params = new URLSearchParams();
    const normalizedQuery = normalizeParamValue(query);
    if (normalizedQuery !== "") {
      params.set("q", normalizedQuery);
    }
    copySharedFilterParams(new URLSearchParams(window.location.search), params);
    const queryString = params.toString();
    return queryString ? `${path}?${queryString}` : path;
  };

  const buildSearchAPIURL = (query, includeSharedFilters) => {
    if (includeSharedFilters) {
      return buildFilteredAPIURL("/api/search", query);
    }
    const params = new URLSearchParams();
    const normalized = normalizeParamValue(query);
    if (normalized !== "") {
      params.set("q", normalized);
    }
    const queryString = params.toString();
    return queryString ? `/api/search?${queryString}` : "/api/search";
  };

  const searchResultPath = (result) => {
    if (!result) {
      return "";
    }
    const path = typeof result.path === "string" ? result.path.trim() : "";
    if (path.startsWith("/n/")) {
      return path;
    }
    return notePathForKey(result.key);
  };

  const initFacets = () => {
    const roots = Array.from(document.querySelectorAll("[data-facet-controls]"));
    if (roots.length === 0) {
      return;
    }

    const makeOptionKey = (option) => `${option.param}\u0000${option.value}`;
    const fallbackGroupForParam = (param) => {
      for (const group of facetGroups) {
        if (group.issue) {
          continue;
        }
        if (group.param === param) {
          return group;
        }
      }
      if (param === "broken" || param === "dead_end" || param === "orphan") {
        return { label: "Issues", issue: true };
      }
      return { label: "Filters" };
    };
    const labelForIssueParam = (param) => {
      switch (param) {
        case "broken":
          return "Broken links";
        case "dead_end":
          return "Dead ends";
        case "orphan":
          return "Orphans";
        default:
          return param;
      }
    };
    const fallbackOption = (param, value) => {
      const group = fallbackGroupForParam(param);
      const label = group.issue ? labelForIssueParam(param) : value;
      return {
        param,
        value,
        label,
        count: 0,
        groupLabel: group.label,
        repeat: repeatedFilterParams.has(param),
      };
    };

    const normalizeFacetOptions = (payload) => {
      const options = [];
      for (const group of facetGroups) {
        const values = Array.isArray(payload && payload[group.key]) ? payload[group.key] : [];
        for (const item of values) {
          if (!item) {
            continue;
          }
          const param = group.issue ? normalizeParamValue(item.param) : group.param;
          const value = normalizeParamValue(item.value);
          if (param === "" || value === "") {
            continue;
          }
          options.push({
            param,
            value,
            label: group.issue ? item.label || labelForIssueParam(param) : value,
            count: Number.isFinite(Number(item.count)) ? Number(item.count) : 0,
            groupLabel: group.label,
            repeat: Boolean(group.repeat),
          });
        }
      }
      return options.sort((left, right) => {
        if (left.groupLabel !== right.groupLabel) {
          return left.groupLabel.localeCompare(right.groupLabel);
        }
        if (right.count !== left.count) {
          return right.count - left.count;
        }
        return left.label.localeCompare(right.label);
      });
    };

    roots.forEach((root) => {
      const input = root.querySelector(".facet-input");
      const optionsEl = root.querySelector(".facet-options");
      const chipsEl = root.querySelector("[data-facet-chips]");
      const inputClear = root.querySelector("[data-facet-input-clear]");
      const clearAll = root.querySelector("[data-facet-clear-all]");
      if (!(input instanceof HTMLInputElement) || !optionsEl || !chipsEl) {
        return;
      }

      let allOptions = [];
      let filteredOptions = [];
      let activeIndex = -1;
      let loaded = false;
      const selected = new Map();

      const setExpanded = (expanded) => {
        input.setAttribute("aria-expanded", expanded ? "true" : "false");
        optionsEl.hidden = !expanded;
      };

      const setActive = (nextIndex) => {
        activeIndex = nextIndex;
        const options = optionsEl.querySelectorAll("[data-facet-index]");
        for (const option of options) {
          const index = Number(option.getAttribute("data-facet-index"));
          const active = index === activeIndex;
          option.classList.toggle("is-active", active);
          if (active) {
            input.setAttribute("aria-activedescendant", option.id);
          }
        }
        if (activeIndex < 0) {
          input.removeAttribute("aria-activedescendant");
        }
      };

      const optionMatchesQuery = (option, query) => {
        if (query === "") {
          return true;
        }
        const haystack = `${option.groupLabel} ${option.label} ${option.value}`.toLowerCase();
        return haystack.includes(query);
      };

      const currentFilterParams = () => {
        const params = new URLSearchParams();
        for (const option of selected.values()) {
          params.append(option.param, option.value);
        }
        return params;
      };

      const renderChips = () => {
        chipsEl.textContent = "";
        const options = Array.from(selected.values());
        chipsEl.hidden = options.length === 0;
        if (clearAll instanceof HTMLButtonElement) {
          clearAll.hidden = options.length === 0;
        }

        for (const option of options) {
          const chip = document.createElement("span");
          chip.className = "facet-chip";

          const text = document.createElement("span");
          text.className = "facet-chip-text";
          text.textContent = `${option.groupLabel}: ${option.label}`;
          chip.appendChild(text);

          if (option.count > 0) {
            const count = document.createElement("span");
            count.className = "facet-chip-count";
            count.textContent = `(${option.count})`;
            chip.appendChild(count);
          }

          const remove = document.createElement("button");
          remove.type = "button";
          remove.className = "facet-chip-remove";
          remove.textContent = "x";
          remove.setAttribute("aria-label", `Remove ${option.groupLabel}: ${option.label}`);
          remove.addEventListener("click", () => {
            selected.delete(makeOptionKey(option));
            renderChips();
            renderOptions();
            emitChange();
            input.focus();
          });
          chip.appendChild(remove);
          chipsEl.appendChild(chip);
        }
      };

      const renderEmptyOption = (message) => {
        optionsEl.textContent = "";
        const item = document.createElement("li");
        item.className = "facet-option-empty";
        item.textContent = message;
        optionsEl.appendChild(item);
        setActive(-1);
      };

      const renderOptions = () => {
        if (inputClear instanceof HTMLButtonElement) {
          inputClear.hidden = input.value.trim() === "";
        }
        if (!loaded) {
          renderEmptyOption("Loading facets...");
          return;
        }

        const query = input.value.trim().toLowerCase();
        filteredOptions = allOptions.filter((option) => optionMatchesQuery(option, query)).slice(0, 80);
        optionsEl.textContent = "";
        if (filteredOptions.length === 0) {
          renderEmptyOption("No matching facets.");
          return;
        }

        filteredOptions.forEach((option, index) => {
          const item = document.createElement("li");
          const button = document.createElement("button");
          const selectedOption = selected.has(makeOptionKey(option));
          button.type = "button";
          button.id = `${input.id || "facet"}-option-${index}`;
          button.className = "facet-option";
          button.setAttribute("role", "option");
          button.setAttribute("aria-selected", selectedOption ? "true" : "false");
          button.setAttribute("data-facet-index", String(index));

          const main = document.createElement("span");
          main.className = "facet-option-main";

          const label = document.createElement("span");
          label.className = "facet-option-label";
          label.textContent = option.label;
          main.appendChild(label);

          const group = document.createElement("span");
          group.className = "facet-option-group";
          group.textContent = option.groupLabel;
          main.appendChild(group);
          button.appendChild(main);

          const count = document.createElement("span");
          count.className = "facet-option-count";
          count.textContent = String(option.count);
          button.appendChild(count);

          button.addEventListener("mouseenter", () => {
            setActive(index);
          });
          button.addEventListener("click", () => {
            toggleOption(option);
          });

          item.appendChild(button);
          optionsEl.appendChild(item);
        });
        setActive(filteredOptions.length > 0 ? Math.min(Math.max(activeIndex, -1), filteredOptions.length - 1) : -1);
      };

      const writeFiltersToURL = () => {
        const params = new URLSearchParams(window.location.search);
        for (const key of sharedFilterParamKeys) {
          params.delete(key);
        }
        for (const [key, value] of currentFilterParams()) {
          params.append(key, value);
        }
        updateBrowserQuery(params);
      };

      function emitChange() {
        writeFiltersToURL();
        window.dispatchEvent(
          new CustomEvent("bn:filterschange", {
            detail: {
              mode: root.getAttribute("data-facet-mode") || "",
              params: currentFilterParams(),
            },
          })
        );
      }

      function toggleOption(option) {
        const key = makeOptionKey(option);
        if (selected.has(key)) {
          selected.delete(key);
        } else {
          if (!option.repeat) {
            for (const selectedOption of Array.from(selected.values())) {
              if (selectedOption.param === option.param) {
                selected.delete(makeOptionKey(selectedOption));
              }
            }
          }
          selected.set(key, option);
        }
        input.value = "";
        setExpanded(true);
        renderChips();
        renderOptions();
        emitChange();
      }

      const readInitialSelection = () => {
        const params = new URLSearchParams(window.location.search);
        for (const param of sharedFilterParamKeys) {
          for (const rawValue of params.getAll(param)) {
            const value = normalizeParamValue(rawValue);
            if (value === "") {
              continue;
            }
            const option = fallbackOption(param, value);
            if (!option.repeat) {
              for (const selectedOption of Array.from(selected.values())) {
                if (selectedOption.param === option.param) {
                  selected.delete(makeOptionKey(selectedOption));
                }
              }
            }
            selected.set(makeOptionKey(option), option);
          }
        }
      };

      const hydrateSelection = () => {
        const catalog = new Map(allOptions.map((option) => [makeOptionKey(option), option]));
        for (const option of Array.from(selected.values())) {
          const hydrated = catalog.get(makeOptionKey(option));
          if (hydrated) {
            selected.set(makeOptionKey(option), hydrated);
          }
        }
      };

      input.addEventListener("focus", () => {
        setExpanded(true);
        renderOptions();
      });
      input.addEventListener("input", () => {
        setExpanded(true);
        setActive(-1);
        renderOptions();
      });
      input.addEventListener("keydown", (event) => {
        if (event.key === "ArrowDown") {
          event.preventDefault();
          setExpanded(true);
          const next = activeIndex < filteredOptions.length - 1 ? activeIndex + 1 : 0;
          setActive(filteredOptions.length > 0 ? next : -1);
          return;
        }
        if (event.key === "ArrowUp") {
          event.preventDefault();
          setExpanded(true);
          const next = activeIndex > 0 ? activeIndex - 1 : filteredOptions.length - 1;
          setActive(filteredOptions.length > 0 ? next : -1);
          return;
        }
        if (event.key === "Enter" && activeIndex >= 0 && activeIndex < filteredOptions.length) {
          event.preventDefault();
          toggleOption(filteredOptions[activeIndex]);
          return;
        }
        if (event.key === "Escape") {
          event.preventDefault();
          input.value = "";
          setExpanded(false);
          setActive(-1);
          renderOptions();
        }
      });

      if (inputClear instanceof HTMLButtonElement) {
        inputClear.addEventListener("click", () => {
          input.value = "";
          setExpanded(true);
          renderOptions();
          input.focus();
        });
      }

      if (clearAll instanceof HTMLButtonElement) {
        clearAll.addEventListener("click", () => {
          selected.clear();
          renderChips();
          renderOptions();
          emitChange();
          input.focus();
        });
      }

      document.addEventListener("click", (event) => {
        const target = event.target;
        if (target instanceof Node && !root.contains(target)) {
          setExpanded(false);
          setActive(-1);
        }
      });

      readInitialSelection();
      renderChips();
      renderOptions();

      fetch("/api/facets", {
        headers: { Accept: "application/json" },
      })
        .then((resp) => {
          if (!resp.ok) {
            throw new Error("facets request failed");
          }
          return resp.json();
        })
        .then((payload) => {
          allOptions = normalizeFacetOptions(payload);
          loaded = true;
          hydrateSelection();
          renderChips();
          renderOptions();
        })
        .catch(() => {
          loaded = true;
          allOptions = [];
          renderEmptyOption("Facets unavailable.");
        });
    });
  };

  const initOpenInEditor = () => {
    const container = document.querySelector("[data-open-editor]");
    if (!container) {
      return;
    }
    const button = container.querySelector("[data-open-button]");
    const statusEl = container.querySelector("[data-open-status]");
    if (!(button instanceof HTMLButtonElement) || !statusEl) {
      return;
    }

    const key = container.getAttribute("data-open-key") || "";
    const csrfToken = container.getAttribute("data-open-csrf") || "";

    const setStatus = (message, kind) => {
      statusEl.textContent = message || " ";
      statusEl.classList.remove("is-success", "is-error");
      if (kind) {
        statusEl.classList.add(kind);
      }
    };

    button.addEventListener("click", async () => {
      if (!key || !csrfToken) {
        setStatus("Open action unavailable.", "is-error");
        return;
      }

      button.disabled = true;
      setStatus("Opening editor…");

      try {
        const body = new URLSearchParams();
        body.set("key", key);
        body.set("token", csrfToken);

        const resp = await fetch("/api/open", {
          method: "POST",
          headers: {
            Accept: "application/json",
            "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
            "X-BN-CSRF": csrfToken,
          },
          body: body.toString(),
        });
        if (!resp.ok) {
          const detail = (await resp.text()).trim();
          setStatus(detail || `Open failed (${resp.status}).`, "is-error");
          return;
        }

        setStatus("Opened in editor. Refresh after saving changes.", "is-success");
      } catch {
        setStatus("Open failed.", "is-error");
      } finally {
        button.disabled = false;
      }
    });
  };

  const initCopyControls = () => {
    const roots = Array.from(document.querySelectorAll("[data-copy-controls]"));
    if (roots.length === 0) {
      return;
    }

    const canWriteClipboard =
      navigator.clipboard && typeof navigator.clipboard.writeText === "function";

    roots.forEach((root) => {
      const statusEl = root.querySelector("[data-copy-status]");
      const buttons = Array.from(root.querySelectorAll("[data-copy-text]"));
      if (!statusEl || buttons.length === 0) {
        return;
      }

      const setStatus = (message, kind) => {
        statusEl.textContent = message || " ";
        statusEl.classList.remove("is-success", "is-error");
        if (kind) {
          statusEl.classList.add(kind);
        }
      };

      buttons.forEach((button) => {
        if (!(button instanceof HTMLButtonElement)) {
          return;
        }

        button.addEventListener("click", async () => {
          const text = button.getAttribute("data-copy-text") || "";
          const label = button.getAttribute("data-copy-label") || "Value";
          if (!text) {
            setStatus("Nothing to copy.", "is-error");
            return;
          }
          if (!canWriteClipboard) {
            setStatus("Clipboard unavailable.", "is-error");
            return;
          }

          button.disabled = true;
          try {
            await navigator.clipboard.writeText(text);
            setStatus(`${label} copied.`, "is-success");
          } catch {
            setStatus("Copy failed.", "is-error");
          } finally {
            button.disabled = false;
          }
        });
      });
    });
  };

  const initGraph = () => {
    const dom = {
      canvas: document.getElementById("graph-canvas"),
      status: document.getElementById("graph-status"),
      detailsPanel: document.querySelector(".graph-hover"),
      hoverTitle: document.getElementById("graph-hover-title"),
      hoverFolder: document.getElementById("graph-hover-folder"),
      hoverDegree: document.getElementById("graph-hover-degree"),
      hoverIssues: document.getElementById("graph-hover-issues"),
      openNote: document.getElementById("graph-open-note"),
      filterInput: document.getElementById("graph-filter-input"),
      spreadInput: document.getElementById("graph-spread-input"),
      spreadValue: document.getElementById("graph-spread-value"),
      fitButton: document.getElementById("graph-fit-button"),
      resetLayoutButton: document.getElementById("graph-reset-layout-button"),
      labelModeInputs: Array.from(document.querySelectorAll('input[name="graph-label-mode"]')),
    };
    if (!dom.canvas) {
      return;
    }

    const statusClasses = ["is-loading", "is-empty", "is-error"];
    const state = {
      cy: null,
      activeLayout: null,
      currentQuery: "",
      currentGraphElements: {
        nodes: [],
        edges: [],
      },
      hoveredNodeID: "",
      selectedNodeID: "",
      labelMode: readLabelMode(),
      lastMeaningfulViewport: null,
      layoutRunID: 0,
    };
    let debounceToken = 0;
    let requestToken = 0;
    let selectionSyncing = false;

    function setStatus(message, kind) {
      if (dom.status) {
        dom.status.textContent = message;
        dom.status.classList.remove(...statusClasses);
        if (kind) {
          dom.status.classList.add(kind);
        }
      }
    }

    function setOpenNoteTarget(nodeID) {
      if (!(dom.openNote instanceof HTMLAnchorElement)) {
        return;
      }

      const path = notePathForKey(nodeID);
      if (!path) {
        dom.openNote.removeAttribute("href");
        dom.openNote.classList.add("is-disabled");
        dom.openNote.setAttribute("aria-disabled", "true");
        dom.openNote.setAttribute("tabindex", "-1");
        return;
      }

      dom.openNote.setAttribute("href", path);
      dom.openNote.classList.remove("is-disabled");
      dom.openNote.setAttribute("aria-disabled", "false");
      dom.openNote.setAttribute("tabindex", "0");
    }

    function formatDegreeDetails(data) {
      if (!data) {
        return "";
      }
      const inDegree = readDegreeValue(data.inDegree, 0);
      const outDegree = readDegreeValue(data.outDegree, 0);
      const degree = readDegreeValue(data.degree, inDegree + outDegree);
      const filteredInDegree = readDegreeValue(data.filteredInDegree, inDegree);
      const filteredOutDegree = readDegreeValue(data.filteredOutDegree, outDegree);
      const filteredDegree = readDegreeValue(data.filteredDegree, filteredInDegree + filteredOutDegree);
      const visibleDetail = `Visible degree: ${filteredDegree} (${filteredInDegree} in, ${filteredOutDegree} out)`;
      if (filteredDegree === degree && filteredInDegree === inDegree && filteredOutDegree === outDegree) {
        return visibleDetail;
      }
      return `${visibleDetail}. Full graph: ${degree} (${inDegree} in, ${outDegree} out)`;
    }

    function issueItemsForNode(data) {
      if (!data) {
        return [];
      }
      const items = [];
      const brokenCount = readDegreeValue(data.brokenCount, 0);
      if (data.hasBroken || brokenCount > 0) {
        items.push({
          className: "is-broken",
          label:
            brokenCount > 0
              ? brokenCount === 1
                ? "1 broken link"
                : `${brokenCount} broken links`
              : "Broken links",
        });
      }
      if (data.isOrphan) {
        items.push({
          className: "is-orphan",
          label: "Orphan",
        });
      }
      if (data.isDeadEnd) {
        items.push({
          className: "is-dead-end",
          label: "Dead end",
        });
      }
      return items;
    }

    function renderIssueDetails(model) {
      if (!dom.hoverIssues) {
        return;
      }
      dom.hoverIssues.textContent = "";
      const items = issueItemsForNode(model);
      dom.hoverIssues.hidden = items.length === 0;
      for (const item of items) {
        const issue = document.createElement("li");
        issue.className = `graph-issue-item ${item.className}`;
        issue.textContent = item.label;
        dom.hoverIssues.appendChild(issue);
      }
    }

    function renderDetailsPanel(fallbackHoveredModel) {
      const selectedModel = findNodeModel(state.selectedNodeID);
      const hoveredModel = selectedModel
        ? null
        : findNodeModel(state.hoveredNodeID) || fallbackHoveredModel || null;
      const model = selectedModel || hoveredModel;

      if (dom.hoverTitle) {
        dom.hoverTitle.textContent = model ? model.title || model.id : "Hover a node";
      }
      if (dom.hoverFolder) {
        dom.hoverFolder.textContent = model ? `Folder: ${model.folder || "/"}` : "";
      }
      if (dom.hoverDegree) {
        dom.hoverDegree.textContent = model ? formatDegreeDetails(model) : "";
      }
      renderIssueDetails(model);
      if (dom.detailsPanel) {
        dom.detailsPanel.classList.toggle("is-locked", Boolean(selectedModel));
        dom.detailsPanel.classList.toggle("is-transient", !selectedModel && Boolean(hoveredModel));
      }
      setOpenNoteTarget(selectedModel ? selectedModel.id : "");
    }

    function hashFolder(folder) {
      let hash = 0;
      for (let i = 0; i < folder.length; i += 1) {
        hash = (hash * 31 + folder.charCodeAt(i)) >>> 0;
      }
      return hash;
    }

    function colorForFolder(folder) {
      if (!folder) {
        return "#7aa2ff";
      }
      const hue = hashFolder(folder) % 360;
      return `hsl(${hue}, 65%, 60%)`;
    }

    function readSpread() {
      if (!(dom.spreadInput instanceof HTMLInputElement)) {
        return 2;
      }
      const parsed = Number.parseFloat(dom.spreadInput.value);
      if (!Number.isFinite(parsed) || parsed <= 0) {
        return 2;
      }
      return parsed;
    }

    function clampedSpread() {
      return Math.min(6, Math.max(0.8, readSpread()));
    }

    function updateSpreadLabel() {
      if (!dom.spreadValue) {
        return;
      }
      const spread = readSpread();
      dom.spreadValue.textContent = `${spread.toFixed(1)}x`;
    }

    function readLabelMode() {
      for (const input of dom.labelModeInputs) {
        if (input instanceof HTMLInputElement && input.checked) {
          return input.value === "all" ? "all" : "auto";
        }
      }
      return "auto";
    }

    function setLabelMode(nextMode) {
      state.labelMode = nextMode === "all" ? "all" : "auto";
      applyGraphVisualState();
    }

    const labelPolicy = {
      smallGraphNodeLimit: 24,
      selectedNeighborLabelLimit: 10,
      zoomLabelMinZoom: 1.05,
      zoomVisibleLabelLimit: 22,
      zoomPriorityLabelLimit: 10,
      deepZoomMinZoom: 1.6,
      deepZoomPriorityLabelLimit: 24,
    };

    const graphStyle = [
      {
        selector: "node",
        style: {
          label: "",
          "background-color": "data(color)",
          color: "#e5e9f0",
          "font-size": "10px",
          "font-weight": 600,
          "text-valign": "bottom",
          "text-halign": "center",
          "text-margin-y": 6,
          "text-wrap": "wrap",
          "text-overflow-wrap": "anywhere",
          "text-max-width": 130,
          "text-outline-width": 1,
          "text-outline-color": "#0b0f16",
          "text-background-color": "#0c1018",
          "text-background-opacity": 0.78,
          "text-background-padding": 2,
          "text-background-shape": "roundrectangle",
          width: "data(size)",
          height: "data(size)",
          "border-width": 0,
          opacity: 1,
          "z-index": 1,
        },
      },
      {
        selector: "node.is-labeled",
        style: {
          label: "data(title)",
        },
      },
      {
        selector: "node.has-broken",
        style: {
          "border-width": 3,
          "border-color": "#ff9d9d",
          shape: "diamond",
        },
      },
      {
        selector: "node.is-orphan",
        style: {
          "border-width": 2,
          "border-style": "dashed",
          "border-color": "#c7d2e7",
        },
      },
      {
        selector: "node.is-dead-end",
        style: {
          "background-blacken": -0.12,
        },
      },
      {
        selector: "node.is-neighbor",
        style: {
          "border-width": 2,
          "border-color": "#8cb8ff",
          "z-index": 8,
        },
      },
      {
        selector: "node.is-hovered",
        style: {
          "border-width": 3,
          "border-color": "#f3f7ff",
          "font-size": "11px",
          "z-index": 20,
        },
      },
      {
        selector: "node.is-selected",
        style: {
          "border-width": 4,
          "border-color": "#ffd166",
          color: "#fff3c2",
          "font-size": "11px",
          "z-index": 30,
        },
      },
      {
        selector: "node.is-dimmed",
        style: {
          opacity: 0.22,
        },
      },
      {
        selector: "edge",
        style: {
          width: 1.5,
          "line-color": "#66718a",
          "target-arrow-color": "#66718a",
          "target-arrow-shape": "triangle",
          "curve-style": "bezier",
          opacity: 0.72,
          "z-index": 0,
        },
      },
      {
        selector: "edge.is-highlighted-edge",
        style: {
          width: 3,
          "line-color": "#8cb8ff",
          "target-arrow-color": "#8cb8ff",
          opacity: 1,
          "z-index": 12,
        },
      },
      {
        selector: "edge.is-dimmed",
        style: {
          opacity: 0.12,
        },
      },
    ];

    function stopActiveLayout() {
      if (state.activeLayout && typeof state.activeLayout.stop === "function") {
        state.activeLayout.stop();
      }
      state.activeLayout = null;
    }

    function layoutOptionsForSpread() {
      const spread = clampedSpread();
      return {
        name: "cose",
        animate: false,
        fit: false,
        randomize: false,
        padding: 72,
        idealEdgeLength: Math.round(70 + 55 * spread),
        nodeRepulsion: Math.round(7000 * spread * spread),
        componentSpacing: Math.round(100 + 85 * spread),
        nodeOverlap: Math.round(12 + 16 * spread),
        gravity: Math.max(0.05, 0.62 - 0.09 * spread),
        numIter: Math.round(900 + 120 * spread),
        initialTemp: Math.round(180 + 45 * spread),
        coolingFactor: 0.95,
        minTemp: 1,
      };
    }

    function currentViewport() {
      if (!state.cy) {
        return null;
      }
      const pan = state.cy.pan();
      return {
        pan: {
          x: pan.x,
          y: pan.y,
        },
        zoom: state.cy.zoom(),
      };
    }

    function applyViewport(viewport) {
      if (!state.cy || !viewport) {
        return;
      }
      state.cy.viewport({
        pan: {
          x: viewport.pan.x,
          y: viewport.pan.y,
        },
        zoom: viewport.zoom,
      });
    }

    function fitCurrentGraph() {
      if (!state.cy || state.cy.nodes().length === 0) {
        return;
      }
      state.cy.fit(state.cy.elements(), 72);
      rememberMeaningfulViewport();
    }

    function rememberMeaningfulViewport() {
      if (!state.cy || state.cy.nodes().length === 0) {
        return;
      }
      state.lastMeaningfulViewport = currentViewport();
    }

    function ensureGraph() {
      if (state.cy) {
        return state.cy;
      }
      state.cy = cytoscape({
        container: dom.canvas,
        elements: [],
        style: graphStyle,
      });
      bindGraphEvents(state.cy);
      return state.cy;
    }

    function bindGraphEvents(instance) {
      instance.on("tap", "node", (event) => {
        selectNode(event.target.id());
      });

      instance.on("tap", (event) => {
        if (event.target === instance) {
          clearSelectedNode();
        }
      });

      instance.on("mouseover", "node", (event) => {
        const data = event.target.data();
        dom.canvas.classList.add("is-node-hovered");
        setHoveredNode(event.target.id(), data);
      });
      instance.on("mouseout", "node", () => {
        dom.canvas.classList.remove("is-node-hovered");
        setHoveredNode("", null);
      });
      instance.on("select", "node", (event) => {
        if (!selectionSyncing) {
          selectNode(event.target.id(), { fromCytoscape: true });
        }
      });
      instance.on("unselect", "node", (event) => {
        if (!selectionSyncing && event.target.id() === state.selectedNodeID) {
          clearSelectedNode({ fromCytoscape: true });
        }
      });
      instance.on("viewport", () => {
        rememberMeaningfulViewport();
        applyGraphVisualState();
      });
    }

    function findNodeModel(nodeID) {
      return state.currentGraphElements.nodes.find((node) => node.id === nodeID) || null;
    }

    function syncCytoscapeSelection(nodeID) {
      if (!state.cy || selectionSyncing) {
        return;
      }

      selectionSyncing = true;
      try {
        state.cy.nodes().forEach((node) => {
          const shouldSelect = node.id() === nodeID;
          if (shouldSelect && !node.selected()) {
            node.select();
          } else if (!shouldSelect && node.selected()) {
            node.unselect();
          }
        });
      } finally {
        selectionSyncing = false;
      }
    }

    function selectNode(nodeID, options) {
      const model = findNodeModel(nodeID);
      state.selectedNodeID = model ? model.id : "";
      if (!options || !options.fromCytoscape) {
        syncCytoscapeSelection(state.selectedNodeID);
      }
      renderDetailsPanel();
      applyGraphVisualState();
    }

    function clearSelectedNode(options) {
      state.selectedNodeID = "";
      if (!options || !options.fromCytoscape) {
        syncCytoscapeSelection("");
      }
      renderDetailsPanel();
      applyGraphVisualState();
    }

    function setHoveredNode(nodeID, fallbackData) {
      state.hoveredNodeID = nodeID || "";
      if (!state.hoveredNodeID) {
        renderDetailsPanel();
        applyGraphVisualState();
        return;
      }

      let fallbackModel = null;
      if (fallbackData) {
        const inDegree = readDegreeValue(fallbackData.inDegree, 0);
        const outDegree = readDegreeValue(fallbackData.outDegree, 0);
        const degree = readDegreeValue(fallbackData.degree, inDegree + outDegree);
        fallbackModel = {
          id: fallbackData.id || state.hoveredNodeID,
          title: fallbackData.title || fallbackData.id || state.hoveredNodeID,
          folder: fallbackData.folder || "",
          inDegree,
          outDegree,
          degree,
          filteredInDegree: readDegreeValue(fallbackData.filteredInDegree, inDegree),
          filteredOutDegree: readDegreeValue(fallbackData.filteredOutDegree, outDegree),
          filteredDegree: readDegreeValue(fallbackData.filteredDegree, degree),
          hasBroken: Boolean(fallbackData.hasBroken),
          brokenCount: readDegreeValue(fallbackData.brokenCount, 0),
          isOrphan: Boolean(fallbackData.isOrphan),
          isDeadEnd: Boolean(fallbackData.isDeadEnd),
        };
      }
      renderDetailsPanel(fallbackModel);
      applyGraphVisualState();
    }

    function keyboardTargetNodeID() {
      if (state.hoveredNodeID && findNodeModel(state.hoveredNodeID)) {
        return state.hoveredNodeID;
      }
      if (state.selectedNodeID && findNodeModel(state.selectedNodeID)) {
        return state.selectedNodeID;
      }
      if (!state.cy) {
        return "";
      }

      const selectedNode = state.cy
        .nodes()
        .filter((node) => node.selected())
        .first();
      return selectedNode && selectedNode.length > 0 ? selectedNode.id() : "";
    }

    function handleGraphKeyDown(event) {
      if (event.key !== "Enter" && event.key !== " " && event.key !== "Spacebar") {
        return;
      }

      const nodeID = keyboardTargetNodeID();
      if (!nodeID) {
        return;
      }

      event.preventDefault();
      selectNode(nodeID);
    }

    function cyNodeByID(nodeID) {
      if (!state.cy || !nodeID) {
        return null;
      }
      const node = state.cy.getElementById(nodeID);
      if (!node || node.length === 0 || !node.isNode()) {
        return null;
      }
      return node;
    }

    function nodeDegree(node) {
      if (!node) {
        return 0;
      }
      const filteredDegree = Number(node.data("filteredDegree"));
      if (Number.isFinite(filteredDegree) && filteredDegree > 0) {
        return filteredDegree;
      }
      const degree = Number(node.data("degree"));
      return Number.isFinite(degree) && degree > 0 ? degree : 0;
    }

    function sortedNodesByImportance(nodes) {
      return nodes
        .slice()
        .sort((left, right) => nodeDegree(right) - nodeDegree(left) || left.id().localeCompare(right.id()));
    }

    function connectedNodeIDs(node) {
      const ids = new Set();
      if (!node) {
        return ids;
      }
      node.connectedEdges().forEach((edge) => {
        edge.connectedNodes().forEach((connectedNode) => {
          const id = connectedNode.id();
          if (id !== node.id()) {
            ids.add(id);
          }
        });
      });
      return ids;
    }

    function visibleNodesForViewport() {
      if (!state.cy) {
        return [];
      }
      const zoom = state.cy.zoom();
      const padding = 72 / Math.max(zoom, 0.1);
      const extent = state.cy.extent();
      return state.cy
        .nodes()
        .filter((node) => {
          const position = node.position();
          return (
            position.x >= extent.x1 - padding &&
            position.x <= extent.x2 + padding &&
            position.y >= extent.y1 - padding &&
            position.y <= extent.y2 + padding
          );
        })
        .toArray();
    }

    function addZoomLabels(labelIDs) {
      if (!state.cy) {
        return;
      }
      const zoom = state.cy.zoom();
      if (zoom < labelPolicy.zoomLabelMinZoom) {
        return;
      }

      const visibleNodes = visibleNodesForViewport();
      if (visibleNodes.length <= labelPolicy.zoomVisibleLabelLimit) {
        for (const node of visibleNodes) {
          labelIDs.add(node.id());
        }
        return;
      }

      const priorityLimit =
        zoom >= labelPolicy.deepZoomMinZoom
          ? labelPolicy.deepZoomPriorityLabelLimit
          : labelPolicy.zoomPriorityLabelLimit;
      for (const node of sortedNodesByImportance(visibleNodes).slice(0, priorityLimit)) {
        labelIDs.add(node.id());
      }
    }

    function labelIDsForCurrentState() {
      const labelIDs = new Set();
      if (!state.cy) {
        return labelIDs;
      }

      if (state.labelMode === "all" || state.cy.nodes().length <= labelPolicy.smallGraphNodeLimit) {
        state.cy.nodes().forEach((node) => {
          labelIDs.add(node.id());
        });
      } else {
        addZoomLabels(labelIDs);
      }

      const selectedNode = cyNodeByID(state.selectedNodeID);
      const hoveredNode = cyNodeByID(state.hoveredNodeID);
      if (selectedNode) {
        labelIDs.add(selectedNode.id());
        const selectedNeighborIDs = connectedNodeIDs(selectedNode);
        if (selectedNeighborIDs.size <= labelPolicy.selectedNeighborLabelLimit) {
          for (const id of selectedNeighborIDs) {
            labelIDs.add(id);
          }
        }
      }
      if (hoveredNode) {
        labelIDs.add(hoveredNode.id());
      }

      return labelIDs;
    }

    function visualFocusState() {
      const selectedNode = cyNodeByID(state.selectedNodeID);
      const hoveredNode = cyNodeByID(state.hoveredNodeID);
      const focusNode = selectedNode || hoveredNode;
      const neighborIDs = connectedNodeIDs(focusNode);
      const highlightedEdgeIDs = new Set();
      if (focusNode) {
        focusNode.connectedEdges().forEach((edge) => {
          highlightedEdgeIDs.add(edge.id());
        });
      }
      return {
        focusNode,
        selectedID: selectedNode ? selectedNode.id() : "",
        hoveredID: hoveredNode ? hoveredNode.id() : "",
        neighborIDs,
        highlightedEdgeIDs,
      };
    }

    function applyGraphVisualState() {
      if (!state.cy) {
        return;
      }
      const labelIDs = labelIDsForCurrentState();
      const focus = visualFocusState();
      state.cy.batch(() => {
        state.cy.nodes().forEach((node) => {
          const id = node.id();
          const isSelected = id === focus.selectedID;
          const isHovered = id === focus.hoveredID;
          const isNeighbor = focus.neighborIDs.has(id);
          const isFocusedNode = focus.focusNode && id === focus.focusNode.id();
          const isDimmed = Boolean(focus.focusNode) && !isFocusedNode && !isHovered && !isNeighbor;
          node.toggleClass("is-labeled", labelIDs.has(id));
          node.toggleClass("is-selected", isSelected);
          node.toggleClass("is-hovered", isHovered);
          node.toggleClass("is-neighbor", isNeighbor);
          node.toggleClass("is-dimmed", isDimmed);
        });

        state.cy.edges().forEach((edge) => {
          const isHighlighted = focus.highlightedEdgeIDs.has(edge.id());
          edge.toggleClass("is-highlighted-edge", isHighlighted);
          edge.toggleClass("is-dimmed", Boolean(focus.focusNode) && !isHighlighted);
        });
      });
    }

    function seedDeterministicPositions() {
      if (!state.cy) {
        return;
      }
      const nodes = state.cy
        .nodes()
        .toArray()
        .sort((left, right) => left.id().localeCompare(right.id()));
      const spread = clampedSpread();
      const radialGap = 52 + 26 * spread;
      const goldenAngle = Math.PI * (3 - Math.sqrt(5));

      nodes.forEach((node, index) => {
        const radius = radialGap * Math.sqrt(index);
        const angle = index * goldenAngle;
        node.position({
          x: Math.cos(angle) * radius,
          y: Math.sin(angle) * radius,
        });
      });
    }

    function separateIsolatedNodes() {
      if (!state.cy) {
        return;
      }
      const isolatedNodes = state.cy
        .nodes()
        .filter((node) => node.connectedEdges().length === 0)
        .toArray()
        .sort((left, right) => left.id().localeCompare(right.id()));
      if (isolatedNodes.length === 0) {
        return;
      }

      const spread = clampedSpread();
      const spacing = 58 + 34 * spread;
      const columns = Math.ceil(Math.sqrt(isolatedNodes.length));
      const rows = Math.ceil(isolatedNodes.length / columns);
      const connectedNodes = state.cy.nodes().filter((node) => node.connectedEdges().length > 0);
      let startX = -((columns - 1) * spacing) / 2;
      let startY = -((rows - 1) * spacing) / 2;

      if (connectedNodes.length > 0) {
        const box = connectedNodes.boundingBox();
        startX = box.x2 + spacing * 1.6;
        startY = box.y1 + (box.h - (rows - 1) * spacing) / 2;
      }

      isolatedNodes.forEach((node, index) => {
        const column = index % columns;
        const row = Math.floor(index / columns);
        node.position({
          x: startX + column * spacing,
          y: startY + row * spacing,
        });
      });
    }

    function runLayout(options) {
      if (!state.cy || state.cy.nodes().length === 0) {
        return;
      }
      const layoutOptions = options || {};
      const fitAfterLayout = Boolean(layoutOptions.fitAfterLayout);
      const preservedViewport = layoutOptions.preserveViewport ? currentViewport() : null;
      const runID = state.layoutRunID + 1;
      state.layoutRunID = runID;
      stopActiveLayout();

      if (layoutOptions.resetPositions) {
        seedDeterministicPositions();
      }

      let finalized = false;
      const finalizeLayout = () => {
        if (finalized || runID !== state.layoutRunID) {
          return;
        }
        finalized = true;
        separateIsolatedNodes();
        if (fitAfterLayout) {
          fitCurrentGraph();
        } else if (preservedViewport) {
          applyViewport(preservedViewport);
          rememberMeaningfulViewport();
        } else {
          rememberMeaningfulViewport();
        }
        applyGraphVisualState();
        state.activeLayout = null;
      };

      state.activeLayout = state.cy.layout(layoutOptionsForSpread());
      if (state.activeLayout && typeof state.activeLayout.one === "function") {
        state.activeLayout.one("layoutstop", finalizeLayout);
      }
      state.activeLayout.run();
      if (!state.activeLayout || typeof state.activeLayout.one !== "function") {
        finalizeLayout();
      }
    }

    function applyGraphElements(graphElements, fitAfterLayout) {
      const instance = ensureGraph();
      stopActiveLayout();
      const selectedNodeID = state.selectedNodeID;

      selectionSyncing = true;
      try {
        instance.elements().remove();
        if (graphElements.nodes.length > 0 || graphElements.edges.length > 0) {
          instance.add(toCytoscapeElements(graphElements));
        }
      } finally {
        selectionSyncing = false;
      }

      state.selectedNodeID = graphElements.nodes.some((node) => node.id === selectedNodeID) ? selectedNodeID : "";
      syncCytoscapeSelection(state.selectedNodeID);
      setHoveredNode("", null);

      if (instance.nodes().length === 0) {
        state.lastMeaningfulViewport = null;
        applyGraphVisualState();
        return;
      }

      runLayout({
        fitAfterLayout,
        resetPositions: true,
      });
    }

    function buildQueryURL(query) {
      return buildFilteredAPIURL("/api/graph.json", query);
    }

    function asArray(value) {
      return Array.isArray(value) ? value : [];
    }

    function readDegreeValue(value, fallback) {
      if (value === undefined || value === null || value === "") {
        return fallback;
      }
      const parsed = Number(value);
      if (Number.isFinite(parsed) && parsed >= 0) {
        return Math.round(parsed);
      }
      return fallback;
    }

    function nodeSizeForDegree(degree) {
      const safeDegree = Number.isFinite(degree) && degree > 0 ? degree : 0;
      return Math.round(Math.min(40, 24 + Math.sqrt(safeDegree) * 4));
    }

    function computeClientDegrees(rawNodes, rawEdges) {
      const degreeSets = new Map();
      for (const node of rawNodes) {
        const data = (node && node.data) || {};
        if (data.id === undefined || data.id === null) {
          continue;
        }
        degreeSets.set(data.id, {
          inSources: new Set(),
          outTargets: new Set(),
        });
      }

      for (const edge of rawEdges) {
        const data = (edge && edge.data) || {};
        if (
          data.source === undefined ||
          data.source === null ||
          data.target === undefined ||
          data.target === null ||
          data.source === data.target
        ) {
          continue;
        }
        if (degreeSets.has(data.source)) {
          degreeSets.get(data.source).outTargets.add(data.target);
        }
        if (degreeSets.has(data.target)) {
          degreeSets.get(data.target).inSources.add(data.source);
        }
      }

      const degrees = new Map();
      for (const [id, sets] of degreeSets) {
        const inDegree = sets.inSources.size;
        const outDegree = sets.outTargets.size;
        degrees.set(id, {
          inDegree,
          outDegree,
          degree: inDegree + outDegree,
        });
      }
      return degrees;
    }

    function normalizeGraphPayload(payload) {
      const rawNodes = asArray(payload && payload.nodes);
      const rawEdges = asArray(payload && payload.edges);
      const fallbackDegrees = computeClientDegrees(rawNodes, rawEdges);

      const nodes = rawNodes.map((node) => {
        const data = (node && node.data) || {};
        const id = data.id;
        const folder = data.folder || "";
        const fallback = fallbackDegrees.get(id) || {
          inDegree: 0,
          outDegree: 0,
          degree: 0,
        };
        const inDegree = readDegreeValue(data.inDegree, fallback.inDegree);
        const outDegree = readDegreeValue(data.outDegree, fallback.outDegree);
        const degree = readDegreeValue(data.degree, inDegree + outDegree);
        const filteredInDegree = readDegreeValue(data.filteredInDegree, inDegree);
        const filteredOutDegree = readDegreeValue(data.filteredOutDegree, outDegree);
        const filteredDegree = readDegreeValue(
          data.filteredDegree,
          filteredInDegree + filteredOutDegree
        );
        const brokenCount = readDegreeValue(data.brokenCount, 0);
        return {
          id,
          title: data.title || id,
          folder,
          color: data.color || colorForFolder(folder),
          inDegree,
          outDegree,
          degree,
          filteredInDegree,
          filteredOutDegree,
          filteredDegree,
          hasBroken: Boolean(data.hasBroken) || brokenCount > 0,
          brokenCount,
          isOrphan: Boolean(data.isOrphan),
          isDeadEnd: Boolean(data.isDeadEnd),
          size: nodeSizeForDegree(filteredDegree),
        };
      });

      const edges = rawEdges.map((edge) => {
        const data = (edge && edge.data) || {};
        return {
          id: data.id,
          source: data.source,
          target: data.target,
        };
      });

      return {
        nodes,
        edges,
      };
    }

    function graphNodeClasses(node) {
      const classes = [];
      if (node.hasBroken) {
        classes.push("has-broken");
      }
      if (node.isOrphan) {
        classes.push("is-orphan");
      }
      if (node.isDeadEnd) {
        classes.push("is-dead-end");
      }
      return classes.join(" ");
    }

    function toCytoscapeElements(graphElements) {
      return [
        ...graphElements.nodes.map((node) => ({
          classes: graphNodeClasses(node),
          data: {
            id: node.id,
            title: node.title,
            folder: node.folder,
            color: node.color,
            inDegree: node.inDegree,
            outDegree: node.outDegree,
            degree: node.degree,
            filteredInDegree: node.filteredInDegree,
            filteredOutDegree: node.filteredOutDegree,
            filteredDegree: node.filteredDegree,
            hasBroken: node.hasBroken,
            brokenCount: node.brokenCount,
            isOrphan: node.isOrphan,
            isDeadEnd: node.isDeadEnd,
            size: node.size,
          },
        })),
        ...graphElements.edges.map((edge) => ({
          data: {
            id: edge.id,
            source: edge.source,
            target: edge.target,
          },
        })),
      ];
    }

    function graphScopeLabel(query, hasFilters) {
      if (query && hasFilters) {
        return `"${query}" with the active filters`;
      }
      if (query) {
        return `"${query}"`;
      }
      if (hasFilters) {
        return "the active filters";
      }
      return "";
    }

    async function loadGraph(query, localToken) {
      if (typeof cytoscape !== "function") {
        setStatus("Graph library failed to load.", "is-error");
        return;
      }

      setStatus("Loading graph...", "is-loading");

      let payload;
      try {
        const resp = await fetch(buildQueryURL(query), {
          headers: { Accept: "application/json" },
        });
        if (localToken !== requestToken) {
          return;
        }
        if (!resp.ok) {
          setStatus(`Graph load failed (${resp.status}).`, "is-error");
          return;
        }
        payload = await resp.json();
      } catch (error) {
        if (localToken !== requestToken) {
          return;
        }
        setStatus("Graph load failed.", "is-error");
        return;
      }

      if (localToken !== requestToken) {
        return;
      }

      state.currentGraphElements = normalizeGraphPayload(payload);
      applyGraphElements(state.currentGraphElements, true);

      const hasFilters = hasActiveSharedFilters();
      const scopeLabel = graphScopeLabel(query, hasFilters);
      if (scopeLabel) {
        if (state.currentGraphElements.nodes.length === 0) {
          setStatus(`No notes match ${scopeLabel}.`, "is-empty");
          return;
        }
        setStatus(
          `Showing ${state.currentGraphElements.nodes.length} nodes and ${state.currentGraphElements.edges.length} edges for ${scopeLabel}.`
        );
        return;
      }

      setStatus(
        `Loaded ${state.currentGraphElements.nodes.length} nodes and ${state.currentGraphElements.edges.length} edges.`
      );
    }

    function scheduleLoad(query, delay) {
      window.clearTimeout(debounceToken);
      state.currentQuery = query;
      const localToken = ++requestToken;
      if (delay <= 0) {
        void loadGraph(query, localToken);
        return;
      }
      debounceToken = window.setTimeout(() => {
        void loadGraph(query, localToken);
      }, delay);
    }

    function bindControls() {
      dom.canvas.addEventListener("keydown", handleGraphKeyDown);

      if (dom.openNote instanceof HTMLAnchorElement) {
        dom.openNote.addEventListener("click", (event) => {
          if (dom.openNote.getAttribute("aria-disabled") === "true") {
            event.preventDefault();
          }
        });
      }

      if (dom.filterInput instanceof HTMLInputElement) {
        dom.filterInput.addEventListener("input", () => {
          const query = dom.filterInput.value.trim();
          syncPageQueryParam(query);
          scheduleLoad(query, 150);
        });
      }

      updateSpreadLabel();
      if (dom.spreadInput instanceof HTMLInputElement) {
        dom.spreadInput.addEventListener("input", () => {
          updateSpreadLabel();
          runLayout({
            fitAfterLayout: false,
            preserveViewport: true,
          });
        });
      }

      if (dom.fitButton instanceof HTMLButtonElement) {
        dom.fitButton.addEventListener("click", fitCurrentGraph);
      }

      if (dom.resetLayoutButton instanceof HTMLButtonElement) {
        dom.resetLayoutButton.addEventListener("click", () => {
          runLayout({
            fitAfterLayout: true,
            resetPositions: true,
          });
        });
      }

      for (const input of dom.labelModeInputs) {
        if (!(input instanceof HTMLInputElement)) {
          continue;
        }
        input.addEventListener("change", () => {
          if (input.checked) {
            setLabelMode(input.value);
          }
        });
      }
    }

    const initialQuery = readPageQuery();
    if (dom.filterInput instanceof HTMLInputElement) {
      dom.filterInput.value = initialQuery;
    }
    window.addEventListener("bn:filterschange", () => {
      const query = dom.filterInput instanceof HTMLInputElement ? dom.filterInput.value.trim() : state.currentQuery;
      scheduleLoad(query, 0);
    });

    bindControls();
    scheduleLoad(initialQuery, 0);
  };

  const initOverviewSearchResults = () => {
    const root = document.querySelector("[data-overview-search-results]");
    if (!root) {
      return;
    }

    const input = document.getElementById("search-input");
    const statusEl = root.querySelector("[data-overview-search-status]");
    const listEl = root.querySelector("[data-overview-search-list]");
    if (!statusEl || !listEl) {
      return;
    }

    const statusClasses = ["is-loading", "is-empty", "is-error"];
    let debounceToken = 0;
    let requestToken = 0;

    const currentQuery = () =>
      input instanceof HTMLInputElement ? normalizeParamValue(input.value) : readPageQuery();

    const setBusy = (busy) => {
      root.setAttribute("aria-busy", busy ? "true" : "false");
    };

    const setStatus = (message, kind) => {
      statusEl.textContent = message;
      statusEl.classList.remove(...statusClasses);
      if (kind) {
        statusEl.classList.add(kind);
      }
    };

    const clearResults = (message, kind) => {
      listEl.textContent = "";
      listEl.hidden = true;
      setStatus(message, kind);
    };

    const renderResult = (result) => {
      const path = searchResultPath(result);
      if (!path) {
        return false;
      }

      const item = document.createElement("li");
      item.className = "overview-result-item";

      const link = document.createElement("a");
      link.className = "overview-result-link";
      link.setAttribute("href", path);

      const title = document.createElement("span");
      title.className = "overview-result-title";
      title.textContent = result.title || result.key || "Untitled";
      link.appendChild(title);

      const meta = document.createElement("span");
      meta.className = "overview-result-meta";

      const folder = document.createElement("span");
      folder.textContent = result.folder ? `Folder: ${result.folder}` : "Folder: /";
      meta.appendChild(folder);

      const matchedFields = Array.isArray(result.matched_fields) ? result.matched_fields : [];
      if (matchedFields.length > 0) {
        const fields = document.createElement("span");
        fields.className = "overview-result-badge";
        fields.textContent = `Matched: ${matchedFields.join(", ")}`;
        meta.appendChild(fields);
      }

      const issues = Array.isArray(result.issues) ? result.issues : [];
      for (const issue of issues) {
        const badge = document.createElement("span");
        badge.className = "overview-result-badge is-issue";
        badge.textContent = String(issue).replace(/_/g, " ");
        meta.appendChild(badge);
      }
      link.appendChild(meta);

      if (result.snippet) {
        const snippet = document.createElement("p");
        snippet.className = "overview-result-snippet";
        snippet.innerHTML = result.snippet;
        link.appendChild(snippet);
      }

      item.appendChild(link);
      listEl.appendChild(item);
      return true;
    };

    const renderResults = (payload, query) => {
      const results = Array.isArray(payload) ? payload : [];
      listEl.textContent = "";

      let renderedCount = 0;
      for (const result of results) {
        if (renderResult(result)) {
          renderedCount += 1;
        }
      }

      if (renderedCount === 0) {
        const message = query ? `No notes match "${query}".` : "No notes match the active filters.";
        clearResults(message, "is-empty");
        return;
      }

      listEl.hidden = false;
      const noun = renderedCount === 1 ? "note" : "notes";
      const scope = query ? ` for "${query}"` : "";
      setStatus(`${renderedCount} matching ${noun}${scope}.`, "");
    };

    const scheduleSearch = (delay) => {
      const query = currentQuery();
      window.clearTimeout(debounceToken);
      syncPageQueryParam(query);

      if (query === "" && !hasActiveSharedFilters()) {
        requestToken += 1;
        setBusy(false);
        clearResults("No query or filters selected.", "is-empty");
        return;
      }

      const localToken = ++requestToken;
      setBusy(true);
      setStatus("Loading matching notes...", "is-loading");
      debounceToken = window.setTimeout(async () => {
        try {
          const resp = await fetch(buildSearchAPIURL(query, true), {
            headers: { Accept: "application/json" },
          });
          if (!resp.ok) {
            throw new Error("search request failed");
          }
          const payload = await resp.json();
          if (localToken !== requestToken) {
            return;
          }
          renderResults(payload, query);
        } catch {
          if (localToken === requestToken) {
            clearResults("Search unavailable.", "is-error");
          }
        } finally {
          if (localToken === requestToken) {
            setBusy(false);
          }
        }
      }, delay);
    };

    if (input instanceof HTMLInputElement) {
      input.value = readPageQuery();
      input.addEventListener("input", () => {
        scheduleSearch(150);
      });
    }

    window.addEventListener("bn:filterschange", (event) => {
      if (event.detail && event.detail.mode && event.detail.mode !== "search") {
        return;
      }
      scheduleSearch(0);
    });

    scheduleSearch(0);
  };

  const initSearch = () => {
    const searchBox = document.querySelector("[data-search-box]");
    const input = document.getElementById("search-input");
    const resultsEl = document.getElementById("search-results");
    if (!searchBox || !input || !resultsEl) {
      return;
    }

    const syncSearchURL = Boolean(document.querySelector('[data-facet-controls][data-facet-mode="search"]'));
    let debounceToken = 0;
    let requestToken = 0;
    let results = [];
    let activeIndex = -1;

    const setExpanded = (expanded) => {
      input.setAttribute("aria-expanded", expanded ? "true" : "false");
    };

    const setActive = (nextIndex) => {
      activeIndex = nextIndex;
      const options = resultsEl.querySelectorAll("[data-search-index]");
      for (const option of options) {
        const index = Number(option.getAttribute("data-search-index"));
        const active = index === activeIndex;
        option.classList.toggle("is-active", active);
        option.setAttribute("aria-selected", active ? "true" : "false");
        if (active) {
          input.setAttribute("aria-activedescendant", option.id);
        }
      }
      if (activeIndex < 0) {
        input.removeAttribute("aria-activedescendant");
      }
    };

    const clearResults = () => {
      results = [];
      resultsEl.textContent = "";
      resultsEl.hidden = true;
      setExpanded(false);
      setActive(-1);
    };

    const hasActiveSearchFilters = () => syncSearchURL && hasActiveSharedFilters();

    const navigateToResult = (index) => {
      if (index < 0 || index >= results.length) {
        return;
      }
      const result = results[index];
      const path = searchResultPath(result);
      if (!path) {
        return;
      }
      window.location.assign(path);
    };

    const renderResults = () => {
      resultsEl.textContent = "";
      if (results.length === 0) {
        clearResults();
        return;
      }

      results.forEach((result, index) => {
        const item = document.createElement("li");
        const button = document.createElement("button");
        button.type = "button";
        button.id = `search-option-${index}`;
        button.className = "search-result";
        button.setAttribute("role", "option");
        button.setAttribute("aria-selected", "false");
        button.setAttribute("data-search-index", String(index));

        const title = document.createElement("span");
        title.className = "search-result-title";
        title.textContent = result.title || result.key || "Untitled";
        button.appendChild(title);

        const folder = document.createElement("span");
        folder.className = "search-result-folder";
        folder.textContent = result.folder ? `Folder: ${result.folder}` : "Folder: /";
        button.appendChild(folder);

        if (result.snippet) {
          const snippet = document.createElement("p");
          snippet.className = "search-result-snippet";
          snippet.innerHTML = result.snippet;
          button.appendChild(snippet);
        }

        button.addEventListener("mouseenter", () => {
          setActive(index);
        });
        button.addEventListener("click", () => {
          navigateToResult(index);
        });

        item.appendChild(button);
        resultsEl.appendChild(item);
      });

      resultsEl.hidden = false;
      setExpanded(true);
      setActive(-1);
    };

    const scheduleSearch = (delay) => {
      const query = input.value.trim();
      window.clearTimeout(debounceToken);
      if (syncSearchURL) {
        syncPageQueryParam(query);
      }

      if (query === "" && !hasActiveSearchFilters()) {
        requestToken += 1;
        clearResults();
        return;
      }

      const localToken = ++requestToken;
      debounceToken = window.setTimeout(async () => {
        try {
          const resp = await fetch(buildSearchAPIURL(query, syncSearchURL), {
            headers: { Accept: "application/json" },
          });
          if (!resp.ok || localToken !== requestToken) {
            return;
          }
          const payload = await resp.json();
          if (localToken !== requestToken) {
            return;
          }
          results = Array.isArray(payload) ? payload : [];
          renderResults();
        } catch {
          if (localToken === requestToken) {
            clearResults();
          }
        }
      }, delay);
    };

    if (syncSearchURL) {
      input.value = readPageQuery();
      if (input.value.trim() !== "" || hasActiveSearchFilters()) {
        scheduleSearch(0);
      }
      window.addEventListener("bn:filterschange", (event) => {
        if (event.detail && event.detail.mode && event.detail.mode !== "search") {
          return;
        }
        scheduleSearch(0);
      });
    }

    input.addEventListener("input", () => {
      scheduleSearch(150);
    });

    input.addEventListener("keydown", (event) => {
      if (results.length === 0) {
        if (event.key === "Escape") {
          clearResults();
        }
        return;
      }

      if (event.key === "ArrowDown") {
        event.preventDefault();
        const next = activeIndex < results.length - 1 ? activeIndex + 1 : 0;
        setActive(next);
        return;
      }
      if (event.key === "ArrowUp") {
        event.preventDefault();
        const next = activeIndex > 0 ? activeIndex - 1 : results.length - 1;
        setActive(next);
        return;
      }
      if (event.key === "Enter" && activeIndex >= 0) {
        event.preventDefault();
        navigateToResult(activeIndex);
        return;
      }
      if (event.key === "Escape") {
        event.preventDefault();
        clearResults();
      }
    });

    document.addEventListener("click", (event) => {
      const target = event.target;
      if (target instanceof Node && !searchBox.contains(target)) {
        clearResults();
      }
    });
  };

  initFacets();
  initOpenInEditor();
  initCopyControls();
  initGraph();
  initSearch();
  initOverviewSearchResults();
})();
