import { initHistoryPanel } from "./history-panel";
import { initFolderSuggestions } from "./folder-suggestions";
// Loaded only by this workspace; shared reading does not initialize it.
import { initLibrarySearch } from "./library-search";
import { initLibrarySearchLink } from "./library-search-link";
import { initLibrarySort } from "./library-sort";
import { initOverviewSearchResults } from "./overview-search";
import { initFacets } from "./facets";
import { initQueryBar } from "./query-bar";

initLibrarySearch();
initLibrarySearchLink();
initLibrarySort();
initOverviewSearchResults();
initFacets();
initQueryBar();

if (document.documentElement.dataset.readOnly !== "true") initFolderSuggestions();

if (document.documentElement.dataset.readOnly !== "true") initHistoryPanel();
