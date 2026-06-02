import {
  fallbackFacetOption,
  type FacetFilterOption,
  normalizeFacetOptions,
} from "./api";
import { queryAllElements, queryElement } from "./dom";
import {
  dispatchFiltersChange,
  normalizeParamValue,
  sharedFilterParamKeys,
  updateBrowserQuery,
} from "./filters";

export type FacetSelection = Map<string, FacetFilterOption>;

export const facetOptionKey = (option: FacetFilterOption): string =>
  `${option.param}\u0000${option.value}`;

export const facetOptionMatchesQuery = (
  option: FacetFilterOption,
  query: string
): boolean => {
  const normalizedQuery = query.trim().toLowerCase();
  if (normalizedQuery === "") {
    return true;
  }
  const haystack = `${option.groupLabel} ${option.label} ${option.value}`.toLowerCase();
  return haystack.includes(normalizedQuery);
};

export const withFacetOptionSelected = (
  selection: FacetSelection,
  option: FacetFilterOption
): FacetSelection => {
  const next = new Map(selection);
  if (!option.repeat) {
    for (const selectedOption of Array.from(next.values())) {
      if (selectedOption.param === option.param) {
        next.delete(facetOptionKey(selectedOption));
      }
    }
  }
  next.set(facetOptionKey(option), option);
  return next;
};

export const facetSelectionFromParams = (params: URLSearchParams): FacetSelection => {
  let selection: FacetSelection = new Map();
  for (const param of sharedFilterParamKeys) {
    for (const rawValue of params.getAll(param)) {
      const value = normalizeParamValue(rawValue);
      if (value === "") {
        continue;
      }
      selection = withFacetOptionSelected(selection, fallbackFacetOption(param, value));
    }
  }
  return selection;
};

export const hydrateFacetSelection = (
  selection: FacetSelection,
  catalogOptions: FacetFilterOption[]
): FacetSelection => {
  const catalog = new Map(catalogOptions.map((option) => [facetOptionKey(option), option]));
  const next: FacetSelection = new Map();
  for (const option of selection.values()) {
    next.set(facetOptionKey(option), catalog.get(facetOptionKey(option)) || option);
  }
  return next;
};

export const paramsForFacetSelection = (selection: FacetSelection): URLSearchParams => {
  const params = new URLSearchParams();
  for (const option of selection.values()) {
    params.append(option.param, option.value);
  }
  return params;
};

export const initFacets = (): void => {
  const roots = queryAllElements(document, "[data-facet-controls]", HTMLElement);
  if (roots.length === 0) {
    return;
  }

  roots.forEach((root) => {
    const input = queryElement(root, ".facet-input", HTMLInputElement);
    const optionsEl = queryElement(root, ".facet-options", HTMLElement);
    const chipsEl = queryElement(root, "[data-facet-chips]", HTMLElement);
    const inputClear = queryElement(root, "[data-facet-input-clear]", HTMLButtonElement);
    const clearAll = queryElement(root, "[data-facet-clear-all]", HTMLButtonElement);
    if (!input || !optionsEl || !chipsEl) {
      return;
    }

    let allOptions: FacetFilterOption[] = [];
    let filteredOptions: FacetFilterOption[] = [];
    let activeIndex = -1;
    let loaded = false;
    let selected = facetSelectionFromParams(new URLSearchParams(window.location.search));

    const setExpanded = (expanded: boolean): void => {
      input.setAttribute("aria-expanded", expanded ? "true" : "false");
      optionsEl.hidden = !expanded;
    };

    const setActive = (nextIndex: number): void => {
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

    const currentFilterParams = (): URLSearchParams => paramsForFacetSelection(selected);

    const renderChips = (): void => {
      chipsEl.textContent = "";
      const options = Array.from(selected.values());
      chipsEl.hidden = options.length === 0;
      if (clearAll) {
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
          selected.delete(facetOptionKey(option));
          renderChips();
          renderOptions();
          emitChange();
          input.focus();
        });
        chip.appendChild(remove);
        chipsEl.appendChild(chip);
      }
    };

    const renderEmptyOption = (message: string): void => {
      optionsEl.textContent = "";
      const item = document.createElement("li");
      item.className = "facet-option-empty";
      item.textContent = message;
      optionsEl.appendChild(item);
      setActive(-1);
    };

    const renderOptions = (): void => {
      if (inputClear) {
        inputClear.hidden = input.value.trim() === "";
      }
      if (!loaded) {
        renderEmptyOption("Loading facets...");
        return;
      }

      const query = input.value.trim().toLowerCase();
      filteredOptions = allOptions
        .filter((option) => facetOptionMatchesQuery(option, query))
        .slice(0, 80);
      optionsEl.textContent = "";
      if (filteredOptions.length === 0) {
        renderEmptyOption("No matching facets.");
        return;
      }

      filteredOptions.forEach((option, index) => {
        const item = document.createElement("li");
        const button = document.createElement("button");
        const selectedOption = selected.has(facetOptionKey(option));
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
      setActive(
        filteredOptions.length > 0
          ? Math.min(Math.max(activeIndex, -1), filteredOptions.length - 1)
          : -1
      );
    };

    const writeFiltersToURL = (): void => {
      const params = new URLSearchParams(window.location.search);
      for (const key of sharedFilterParamKeys) {
        params.delete(key);
      }
      for (const [key, value] of currentFilterParams()) {
        params.append(key, value);
      }
      updateBrowserQuery(params);
    };

    function emitChange(): void {
      writeFiltersToURL();
      dispatchFiltersChange(window, {
        mode: root.getAttribute("data-facet-mode") || "",
        params: currentFilterParams(),
      });
    }

    function toggleOption(option: FacetFilterOption): void {
      const key = facetOptionKey(option);
      if (selected.has(key)) {
        selected.delete(key);
      } else {
        selected = withFacetOptionSelected(selected, option);
      }
      input!.value = "";
      setExpanded(true);
      renderChips();
      renderOptions();
      emitChange();
    }

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

    if (inputClear) {
      inputClear.addEventListener("click", () => {
        input.value = "";
        setExpanded(true);
        renderOptions();
        input.focus();
      });
    }

    if (clearAll) {
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

    renderChips();
    renderOptions();

    fetch("/api/facets", {
      headers: { Accept: "application/json" },
    })
      .then((resp: Response) => {
        if (!resp.ok) {
          throw new Error("facets request failed");
        }
        return resp.json();
      })
      .then((payload: unknown) => {
        allOptions = normalizeFacetOptions(payload);
        loaded = true;
        selected = hydrateFacetSelection(selected, allOptions);
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
