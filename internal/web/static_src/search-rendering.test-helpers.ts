import { expect } from "vitest";

export const renderOverviewBulkFixture = (): void => {
  document.body.innerHTML = `
    <input id="search-input" />
    <section data-overview-search-results aria-busy="false">
      <p data-overview-search-status></p>
      <div data-overview-bulk data-overview-csrf="csrf-token" hidden>
        <p data-overview-selected-count></p>
        <button type="button" data-bulk-trash>Trash</button>
        <form data-bulk-move>
          <input name="folder" type="text" />
          <button type="submit">Move</button>
        </form>
        <form data-bulk-frontmatter-set>
          <input name="property" type="text" />
          <select name="op">
            <option value="=">=</option>
            <option value="+=">+=</option>
            <option value="-=">-=</option>
          </select>
          <input name="value" type="text" />
          <button type="submit">Apply</button>
        </form>
        <form data-bulk-frontmatter-unset>
          <input name="property" type="text" />
          <button type="submit">Unset</button>
        </form>
        <section data-overview-bulk-preview hidden>
          <h3 data-overview-bulk-preview-title>Bulk preview</h3>
          <p data-overview-bulk-preview-message></p>
          <button type="button" data-overview-bulk-preview-apply>Apply</button>
          <button type="button" data-overview-bulk-preview-clear>Clear</button>
          <dl data-overview-bulk-preview-counts></dl>
          <ul data-overview-bulk-preview-results></ul>
          <ul data-overview-bulk-preview-failures></ul>
        </section>
        <p data-overview-bulk-status></p>
      </div>
      <ul data-overview-search-list hidden></ul>
    </section>
  `;
};

export const selectOverviewResult = (): HTMLInputElement => {
  const checkbox = document.querySelector<HTMLInputElement>(".overview-result-select input");
  expect(checkbox).not.toBeNull();
  checkbox!.checked = true;
  checkbox!.dispatchEvent(new Event("change", { bubbles: true }));
  return checkbox!;
};
