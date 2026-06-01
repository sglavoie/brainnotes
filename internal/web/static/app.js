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
    const dom = {
      canvas: document.getElementById("graph-canvas"),
      status: document.getElementById("graph-status"),
      detailsPanel: document.querySelector(".graph-hover"),
      hoverTitle: document.getElementById("graph-hover-title"),
      hoverFolder: document.getElementById("graph-hover-folder"),
      hoverDegree: document.getElementById("graph-hover-degree"),
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
      return `Degree: ${degree} (${inDegree} in, ${outDegree} out)`;
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

      const fallbackModel = fallbackData
        ? {
            id: fallbackData.id || state.hoveredNodeID,
            title: fallbackData.title || fallbackData.id || state.hoveredNodeID,
            folder: fallbackData.folder || "",
            inDegree: fallbackData.inDegree || 0,
            outDegree: fallbackData.outDegree || 0,
            degree: fallbackData.degree || 0,
          }
        : null;
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
      if (!query) {
        return "/api/graph.json";
      }
      return `/api/graph.json?q=${encodeURIComponent(query)}`;
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
        return {
          id,
          title: data.title || id,
          folder,
          color: data.color || colorForFolder(folder),
          inDegree,
          outDegree,
          degree,
          size: nodeSizeForDegree(degree),
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

    function toCytoscapeElements(graphElements) {
      return [
        ...graphElements.nodes.map((node) => ({
          data: {
            id: node.id,
            title: node.title,
            folder: node.folder,
            color: node.color,
            inDegree: node.inDegree,
            outDegree: node.outDegree,
            degree: node.degree,
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

      if (query) {
        if (state.currentGraphElements.nodes.length === 0) {
          setStatus(`No notes match "${query}".`, "is-empty");
          return;
        }
        setStatus(
          `Showing ${state.currentGraphElements.nodes.length} nodes and ${state.currentGraphElements.edges.length} edges for "${query}".`
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
          scheduleLoad(dom.filterInput.value.trim(), 150);
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

    bindControls();
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
