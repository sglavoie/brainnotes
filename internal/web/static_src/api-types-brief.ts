// Daily Brief -----------------------------------------------------------------

export type BriefSlotKind = "continue" | "rediscover" | "connect";

export type BriefNoteRef = {
  RelPath: string;
  Title: string;
};

export type BriefCandidateMetadata = {
  Score: number;
  Relationship: string;
  Bridge: string;
  Reasons: string[];
};

export type BriefCard = {
  ID: string;
  Slot: string;
  Source: BriefNoteRef;
  Candidate?: BriefNoteRef;
  Explanation: string;
  Excerpt: string;
  Metadata: BriefCandidateMetadata;
};

export type BriefUnavailableSlot = {
  Slot: string;
  Message: string;
};

export type BriefSlotResult = {
  Slot: string;
  Card?: BriefCard;
  Unavailable?: BriefUnavailableSlot;
};

export type BriefDaily = {
  Date: string;
  Slots: BriefSlotResult[];
};

export type BriefActivitySummary = {
  WindowDays: number;
  VisitDays: number;
  OpenCount: number;
};

export type BriefRollingActivity = {
  Last7Days: BriefActivitySummary;
  Last14Days: BriefActivitySummary;
};

export type BriefViewResult = {
  ok: boolean;
  code?: string;
  message?: string;
  activity: BriefRollingActivity;
};

export type BriefItemAction = "open" | "another";

export type BriefItemResult = {
  ok: boolean;
  code?: string;
  message?: string;
  action: string;
  card_id: string;
  slot: string;
  daily?: BriefDaily;
  activity: BriefRollingActivity;
  affected: BriefSlotResult[];
};

export type BriefInsightResult = {
  ok: boolean;
  code?: string;
  message?: string;
  enabled: boolean;
  card_id: string;
  slot: string;
  output: string;
  rendered_html: string;
};

export type BriefViewRequest = {
  token: string;
};

export type BriefItemRequest = {
  cardID: string;
  action: BriefItemAction;
  token: string;
};

export type BriefInsightRequest = {
  cardID: string;
  prompt: string;
  token: string;
};
