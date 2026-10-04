import { normalizeFacetOptions, type FacetFilterOption } from "./api";
import { dispatchFiltersChange, sharedFilterParamKeys, updateBrowserQuery } from "./filters";
import {
  facetOptionKey, facetOptionMatchesQuery, facetSelectionFromParams,
  hydrateFacetSelection, paramsForFacetSelection, withFacetOptionSelected,
} from "./facet-model";
import { renderFacetChip, renderFacetOption } from "./facet-render";

export class FacetController {
  private allOptions: FacetFilterOption[] = [];
  private filteredOptions: FacetFilterOption[] = [];
  private activeIndex = -1;
  private state: "loading" | "ready" | "error" = "loading";
  private inFlight = false;
  private selected = facetSelectionFromParams(new URLSearchParams(window.location.search));
  private inputClear: HTMLButtonElement | null;
  private clearAll: HTMLButtonElement | null;
  private retry: HTMLButtonElement | null;
  private status: HTMLElement | null;

  constructor(
    private root: HTMLElement,
    private input: HTMLInputElement,
    private options: HTMLElement,
    private chips: HTMLElement
  ) {
    this.inputClear = root.querySelector("[data-facet-input-clear]");
    this.clearAll = root.querySelector("[data-facet-clear-all]");
    this.retry = root.querySelector("[data-facet-retry]");
    this.status = root.querySelector("[data-facet-status]");
  }

  init(): void {
    this.input.addEventListener("focus", () => this.showOptions());
    this.input.addEventListener("input", () => {
      this.setActive(-1);
      this.showOptions();
    });
    this.input.addEventListener("keydown", (event) => this.onKeyDown(event));
    this.inputClear?.addEventListener("click", () => {
      this.input.value = "";
      this.showOptions();
      this.input.focus();
    });
    this.clearAll?.addEventListener("click", () => {
      this.selected.clear();
      this.selectionChanged();
    });
    this.retry?.addEventListener("click", () => { void this.load(true); });
    document.addEventListener("click", (event) => {
      if (event.target instanceof Node && !this.root.contains(event.target)) {
        this.setExpanded(false);
        this.setActive(-1);
      }
    });
    this.renderChips();
    void this.load();
  }

  private async load(retrying = false): Promise<void> {
    if (this.inFlight) return;
    this.inFlight = true;
    this.state = "loading";
    this.setLoadFeedback(retrying ? "Loading filters…" : "", retrying, true);
    this.renderOptions();
    try {
      const response = await fetch("/api/facets", { headers: { Accept: "application/json" } });
      if (!response.ok) throw new Error("facets request failed");
      this.allOptions = normalizeFacetOptions(await response.json());
      this.selected = hydrateFacetSelection(this.selected, this.allOptions);
      this.state = "ready";
      this.renderChips();
      // Move focus before hiding the retry button, only if it still owns focus.
      if (retrying && document.activeElement === this.retry) this.input.focus();
      this.setLoadFeedback(retrying ? "Filters loaded." : "", false, false);
    } catch {
      this.state = "error";
      this.setLoadFeedback("Filters unavailable. Your selected filters are still applied.", true, false);
    } finally {
      this.inFlight = false;
      this.renderOptions();
    }
  }

  private setLoadFeedback(message: string, showRetry: boolean, disabled: boolean): void {
    if (this.status) this.status.textContent = message;
    if (this.retry) {
      this.retry.hidden = !showRetry;
      // Keep keyboard focus during the request; load's inFlight guard prevents
      // repeated activation. Native disabled buttons lose focus in browsers.
      this.retry.setAttribute("aria-disabled", String(disabled));
    }
  }

  private setExpanded(expanded: boolean): void {
    this.input.setAttribute("aria-expanded", String(expanded));
    this.options.hidden = !expanded;
  }

  private showOptions(): void {
    this.setExpanded(true);
    this.renderOptions();
  }

  private setActive(index: number): void {
    this.activeIndex = index;
    this.input.removeAttribute("aria-activedescendant");
    for (const option of this.options.querySelectorAll<HTMLElement>("[data-facet-index]")) {
      const active = Number(option.dataset.facetIndex) === index;
      option.classList.toggle("is-active", active);
      if (active) this.input.setAttribute("aria-activedescendant", option.id);
    }
  }

  private renderChips(): void {
    this.chips.replaceChildren();
    this.chips.hidden = this.selected.size === 0;
    if (this.clearAll) this.clearAll.hidden = this.selected.size === 0;
    for (const option of this.selected.values()) {
      this.chips.append(renderFacetChip(option, () => {
        this.selected.delete(facetOptionKey(option));
        this.selectionChanged();
      }));
    }
  }

  private renderEmpty(message: string): void {
    const item = document.createElement("li");
    item.className = "facet-option-empty";
    item.textContent = message;
    this.options.replaceChildren(item);
    this.filteredOptions = [];
    this.setActive(-1);
  }

  private renderOptions(): void {
    if (this.inputClear) this.inputClear.hidden = this.input.value.trim() === "";
    if (this.state !== "ready") {
      this.renderEmpty(this.state === "error" ? "Facets unavailable." : "Loading facets...");
      return;
    }
    this.filteredOptions = this.allOptions
      .filter((option) => facetOptionMatchesQuery(option, this.input.value)).slice(0, 80);
    if (!this.filteredOptions.length) {
      this.renderEmpty("No matching facets.");
      return;
    }
    this.options.replaceChildren();
    this.filteredOptions.forEach((option, index) => {
      this.options.append(renderFacetOption({
        option, index, inputID: this.input.id || "facet",
        selected: this.selected.has(facetOptionKey(option)),
        activate: () => this.setActive(index),
        toggle: () => this.toggleOption(option),
      }));
    });
    this.setActive(Math.min(Math.max(this.activeIndex, -1), this.filteredOptions.length - 1));
  }

  private selectionChanged(): void {
    this.renderChips();
    this.renderOptions();
    const selectedParams = paramsForFacetSelection(this.selected);
    const params = new URLSearchParams(window.location.search);
    for (const key of sharedFilterParamKeys) params.delete(key);
    for (const [key, value] of selectedParams) params.append(key, value);
    updateBrowserQuery(params);
    dispatchFiltersChange(window, {
      mode: this.root.getAttribute("data-facet-mode") || "", params: selectedParams,
    });
    this.input.focus();
  }

  private toggleOption(option: FacetFilterOption): void {
    const key = facetOptionKey(option);
    if (this.selected.has(key)) this.selected.delete(key);
    else this.selected = withFacetOptionSelected(this.selected, option);
    this.input.value = "";
    this.setExpanded(true);
    this.selectionChanged();
  }

  private moveActive(direction: number): void {
    const count = this.filteredOptions.length;
    const index = this.activeIndex + direction;
    this.setActive(count ? (index + count) % count : -1);
  }

  private onKeyDown(event: KeyboardEvent): void {
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      this.setExpanded(true);
      // Up from no selection starts at the last option.
      if (event.key === "ArrowUp" && this.activeIndex < 0) this.setActive(this.filteredOptions.length - 1);
      else this.moveActive(event.key === "ArrowDown" ? 1 : -1);
    } else if (event.key === "Enter" && this.activeIndex >= 0) {
      event.preventDefault();
      this.toggleOption(this.filteredOptions[this.activeIndex]);
    } else if (event.key === "Escape") {
      event.preventDefault();
      this.input.value = "";
      this.setExpanded(false);
      this.setActive(-1);
      this.renderOptions();
    }
  }
}
