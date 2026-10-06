import type { RelatedFeedback, RelatedMode, RelatedResponse } from "./related-types";

export const RELATED_INITIAL_LIMIT = 5;
export const RELATED_LIMIT_STEP = 5;
export const RELATED_MAX_LIMIT = 20;

export type RelatedModeDefinition = {
  mode: RelatedMode;
  label: string;
  description: string;
};

export const relatedModeDefinitions: readonly RelatedModeDefinition[] = [
  {
    mode: "balanced",
    label: "Balanced",
    description: "A broad mix of the strongest connections.",
  },
  {
    mode: "rediscover",
    label: "Rediscover",
    description: "Older notes that may be worth revisiting.",
  },
  {
    mode: "connect",
    label: "Connect",
    description: "Notes connected through shared context.",
  },
];

export type RelatedModeState = {
  limit: number;
  loaded: boolean;
  loading: boolean;
  response?: RelatedResponse;
  error: string;
  failedLimit?: number;
  stale: boolean;
};

export type RelatedModel = {
  activeMode: RelatedMode;
  modes: Record<RelatedMode, RelatedModeState>;
  feedback: RelatedFeedback[];
  serverDate: string;
};

const createModeState = (): RelatedModeState => ({
  limit: RELATED_INITIAL_LIMIT,
  loaded: false,
  loading: false,
  error: "",
  stale: false,
});

export const createRelatedModel = (): RelatedModel => ({
  activeMode: "balanced",
  modes: {
    balanced: createModeState(),
    rediscover: createModeState(),
    connect: createModeState(),
  },
  feedback: [],
  serverDate: "",
});

export const nextRelatedLimit = (limit: number): number =>
  Math.min(RELATED_MAX_LIMIT, limit + RELATED_LIMIT_STEP);

const parseCalendarDate = (value: string): [number, number, number] | null => {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value);
  if (!match) {
    return null;
  }
  const year = Number(match[1]);
  const month = Number(match[2]);
  const day = Number(match[3]);
  const date = new Date(Date.UTC(year, month - 1, day));
  if (
    date.getUTCFullYear() !== year ||
    date.getUTCMonth() !== month - 1 ||
    date.getUTCDate() !== day
  ) {
    return null;
  }
  return [year, month, day];
};

const formatCalendarDate = (date: Date): string =>
  `${date.getUTCFullYear().toString().padStart(4, "0")}-${(date.getUTCMonth() + 1)
    .toString()
    .padStart(2, "0")}-${date.getUTCDate().toString().padStart(2, "0")}`;

export const addRelatedCalendarDays = (serverDate: string, days: number): string => {
  const parsed = parseCalendarDate(serverDate);
  if (!parsed) {
    return "";
  }
  const [year, month, day] = parsed;
  const date = new Date(Date.UTC(year, month - 1, day));
  date.setUTCDate(date.getUTCDate() + Math.trunc(days));
  return formatCalendarDate(date);
};

export const validateRelatedLaterDate = (serverDate: string, value: string): string => {
  const parsed = parseCalendarDate(value);
  const minimum = addRelatedCalendarDays(serverDate, 1);
  if (!parsed || minimum === "" || value < minimum) {
    return "Choose a valid date after the server date.";
  }
  return "";
};

export const relatedRelationshipLabel = (relationship: string): string => {
  switch (relationship) {
    case "direct":
      return "Directly linked";
    case "shared-neighbor":
      return "Shared neighbor";
    case "two-hop":
      return "Two links away";
    default:
      return "Related by content or metadata";
  }
};

export const relatedReasonLabel = (kind: string): string => {
  switch (kind) {
    case "text":
      return "Text match";
    case "graph":
      return "Link structure";
    case "metadata":
      return "Shared metadata";
    case "recall":
      return "Recall history";
    default:
      return "Reason";
  }
};
