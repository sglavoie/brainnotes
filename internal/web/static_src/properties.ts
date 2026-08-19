import { initPropertiesCatalog } from "./properties-catalog";
import { initPropertyDetail } from "./properties-detail";
import { type PropertiesDeps } from "./properties-model";

export {
  buildOpenConfigFetchRequest,
  inferredDefinition,
  initPropertiesCatalog,
} from "./properties-catalog";
export { initPropertyDetail } from "./properties-detail";
export {
  catalogStatusMessage,
  configStatusMessage,
  coverageLabel,
  definitionLabel,
  detailStatusMessage,
  filterCatalog,
  issueLabel,
  issueSummaryLabel,
  nextOffset,
  paginationLabel,
  previousOffset,
  propertyKindLabel,
  readPropertiesConfig,
  readPropertyDetailConfig,
  valueDisplayText,
  type CatalogFilter,
  type DetailFilter,
  type PropertiesConfig,
  type PropertiesDeps,
  type PropertyDetailConfig,
} from "./properties-model";
export { renderCatalog, renderProposals, renderRows, renderValues } from "./properties-render";

export const initProperties = (deps: PropertiesDeps = {}): void => {
  initPropertiesCatalog(deps);
  initPropertyDetail(deps);
};
