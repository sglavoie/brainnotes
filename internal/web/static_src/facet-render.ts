import type { FacetFilterOption } from "./api";

export const renderFacetChip = (option: FacetFilterOption, remove: () => void): HTMLElement => {
  const chip = document.createElement("span");
  chip.className = "facet-chip";
  const text = document.createElement("span");
  text.className = "facet-chip-text";
  text.textContent = `${option.groupLabel}: ${option.label}`;
  chip.append(text);
  if (option.count > 0) {
    const count = document.createElement("span");
    count.className = "facet-chip-count";
    count.textContent = `(${option.count})`;
    chip.append(count);
  }
  const button = document.createElement("button");
  button.type = "button";
  button.className = "facet-chip-remove";
  button.textContent = "x";
  button.setAttribute("aria-label", `Remove ${option.groupLabel}: ${option.label}`);
  button.addEventListener("click", remove);
  chip.append(button);
  return chip;
};

type OptionView = {
  option: FacetFilterOption;
  index: number;
  inputID: string;
  selected: boolean;
  activate(): void;
  toggle(): void;
};

export const renderFacetOption = (view: OptionView): HTMLElement => {
  const { option } = view;
  const item = document.createElement("li");
  const button = document.createElement("button");
  button.type = "button";
  button.id = `${view.inputID}-option-${view.index}`;
  button.className = "facet-option";
  button.setAttribute("role", "option");
  button.setAttribute("aria-selected", String(view.selected));
  button.dataset.facetIndex = String(view.index);
  const main = document.createElement("span");
  main.className = "facet-option-main";
  const label = document.createElement("span");
  label.className = "facet-option-label";
  label.textContent = option.label;
  const group = document.createElement("span");
  group.className = "facet-option-group";
  group.textContent = option.groupLabel;
  main.append(label, group);
  const count = document.createElement("span");
  count.className = "facet-option-count";
  count.textContent = String(option.count);
  button.append(main, count);
  button.addEventListener("mouseenter", view.activate);
  button.addEventListener("click", view.toggle);
  item.append(button);
  return item;
};
