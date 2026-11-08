// Loaded only by this workspace; shared reading does not initialize it.
import { initDailyBrief } from "./daily-brief";
import { enhanceBriefCards } from "./brief-experience";

initDailyBrief();
enhanceBriefCards(document);
