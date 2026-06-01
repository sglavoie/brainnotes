(function () {
  const notePathForKey = (key) => {
    if (!key) {
      return "";
    }
    const segments = String(key).split("/");
    const escaped = segments.map((segment) => encodeURIComponent(segment));
    return `/n/${escaped.join("/")}`;
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

  const initGraph = () => {
    const canvas = document.getElementById("graph-canvas");
    if (!canvas) {
      return;
    }

    const statusEl = document.getElementById("graph-status");
    const hoverTitleEl = document.getElementById("graph-hover-title");
    const hoverFolderEl = document.getElementById("graph-hover-folder");
    const filterInputEl = document.getElementById("graph-filter-input");
    const spreadInputEl = document.getElementById("graph-spread-input");
    const spreadValueEl = document.getElementById("graph-spread-value");
    const statusClasses = ["is-loading", "is-empty", "is-error"];

    let cy = null;
    let activeLayout = null;
    let debounceToken = 0;
    let requestToken = 0;

    const setStatus = (message, kind) => {
      if (statusEl) {
        statusEl.textContent = message;
        statusEl.classList.remove(...statusClasses);
        if (kind) {
          statusEl.classList.add(kind);
        }
      }
    };

    const setHover = (title, folder) => {
      if (hoverTitleEl) {
        hoverTitleEl.textContent = title || "Hover a node";
      }
      if (hoverFolderEl) {
        hoverFolderEl.textContent = folder ? `Folder: ${folder}` : "";
      }
    };

    const hashFolder = (folder) => {
      let hash = 0;
      for (let i = 0; i < folder.length; i += 1) {
        hash = (hash * 31 + folder.charCodeAt(i)) >>> 0;
      }
      return hash;
    };

    const colorForFolder = (folder) => {
      if (!folder) {
        return "#7aa2ff";
      }
      const hue = hashFolder(folder) % 360;
      return `hsl(${hue}, 65%, 60%)`;
    };

    const readSpread = () => {
      if (!(spreadInputEl instanceof HTMLInputElement)) {
        return 1;
      }
      const parsed = Number.parseFloat(spreadInputEl.value);
      if (!Number.isFinite(parsed) || parsed <= 0) {
        return 1;
      }
      return parsed;
    };

    const updateSpreadLabel = () => {
      if (!spreadValueEl) {
        return;
      }
      const spread = readSpread();
      spreadValueEl.textContent = `${spread.toFixed(1)}x`;
    };

    const graphStyle = [
      {
        selector: "node",
        style: {
          label: "data(title)",
          "background-color": "data(color)",
          color: "#e5e9f0",
          "font-size": "10px",
          "text-outline-width": 2,
          "text-outline-color": "#0f131c",
          width: 24,
          height: 24,
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
        },
      },
    ];

    const stopActiveLayout = () => {
      if (activeLayout && typeof activeLayout.stop === "function") {
        activeLayout.stop();
      }
      activeLayout = null;
    };

    const layoutOptionsForSpread = (fitToGraph) => {
      const spread = readSpread();
      return {
        name: "cose",
        animate: false,
        fit: fitToGraph,
        padding: 24,
        idealEdgeLength: Math.round(80 * spread),
        nodeRepulsion: Math.round(2800 * spread * spread),
        gravity: Math.max(0.2, 1.3 - 0.35 * spread),
      };
    };

    const ensureGraph = () => {
      if (cy) {
        return cy;
      }
      cy = cytoscape({
        container: canvas,
        elements: [],
        style: graphStyle,
      });

      cy.on("tap", "node", (event) => {
        const key = event.target.id();
        const path = notePathForKey(key);
        if (!path) {
          return;
        }
        window.location.assign(path);
      });

      cy.on("mouseover", "node", (event) => {
        const data = event.target.data();
        setHover(data.title || data.id, data.folder || "");
      });
      cy.on("mouseout", "node", () => {
        setHover("", "");
      });

      return cy;
    };

    const applyGraphElements = (nodes, edges, fitToGraph) => {
      const instance = ensureGraph();
      stopActiveLayout();
      instance.elements().remove();
      if (nodes.length > 0 || edges.length > 0) {
        instance.add([...nodes, ...edges]);
      }
      setHover("", "");

      if (instance.nodes().length === 0) {
        return;
      }

      activeLayout = instance.layout(layoutOptionsForSpread(fitToGraph));
      activeLayout.run();
    };

    const buildQueryURL = (query) => {
      if (!query) {
        return "/api/graph.json";
      }
      return `/api/graph.json?q=${encodeURIComponent(query)}`;
    };

    const loadGraph = async (query, localToken) => {
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

      const nodes = (payload.nodes || []).map((node) => {
        const data = node.data || {};
        return {
          data: {
            id: data.id,
            title: data.title || data.id,
            folder: data.folder || "",
            color: colorForFolder(data.folder || ""),
          },
        };
      });
      const edges = (payload.edges || []).map((edge) => {
        const data = edge.data || {};
        return {
          data: {
            id: data.id,
            source: data.source,
            target: data.target,
          },
        };
      });

      applyGraphElements(nodes, edges, true);

      if (query) {
        if (nodes.length === 0) {
          setStatus(`No notes match "${query}".`, "is-empty");
          return;
        }
        setStatus(`Showing ${nodes.length} nodes and ${edges.length} edges for "${query}".`);
        return;
      }

      setStatus(`Loaded ${nodes.length} nodes and ${edges.length} edges.`);
    };

    const scheduleLoad = (query, delay) => {
      window.clearTimeout(debounceToken);
      const localToken = ++requestToken;
      if (delay <= 0) {
        void loadGraph(query, localToken);
        return;
      }
      debounceToken = window.setTimeout(() => {
        void loadGraph(query, localToken);
      }, delay);
    };

    if (filterInputEl instanceof HTMLInputElement) {
      filterInputEl.addEventListener("input", () => {
        scheduleLoad(filterInputEl.value.trim(), 150);
      });
    }

    updateSpreadLabel();
    if (spreadInputEl instanceof HTMLInputElement) {
      spreadInputEl.addEventListener("input", () => {
        updateSpreadLabel();
        if (!cy || cy.nodes().length === 0) {
          return;
        }
        stopActiveLayout();
        activeLayout = cy.layout(layoutOptionsForSpread(false));
        activeLayout.run();
      });
    }

    scheduleLoad("", 0);
  };

  const initSearch = () => {
    const searchBox = document.querySelector("[data-search-box]");
    const input = document.getElementById("search-input");
    const resultsEl = document.getElementById("search-results");
    if (!searchBox || !input || !resultsEl) {
      return;
    }

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

    const navigateToResult = (index) => {
      if (index < 0 || index >= results.length) {
        return;
      }
      const result = results[index];
      const path =
        typeof result.path === "string" && result.path
          ? result.path
          : notePathForKey(result.key);
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

    input.addEventListener("input", () => {
      const query = input.value.trim();
      window.clearTimeout(debounceToken);

      if (query === "") {
        clearResults();
        return;
      }

      const localToken = ++requestToken;
      debounceToken = window.setTimeout(async () => {
        try {
          const resp = await fetch(`/api/search?q=${encodeURIComponent(query)}`, {
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
      }, 150);
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

  initOpenInEditor();
  initGraph();
  initSearch();
})();
