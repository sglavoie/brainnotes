import type { SettingSummary } from "./api";
import { bytesAsMiB } from "./settings-units";
import { formatSettingValue } from "./settings-model";

const groups: Record<string, string> = {
  capture: "Quick capture", templates: "Note templates", attachments: "Attachments",
  brief: "Daily Brief", related: "Related notes", cockpit: "Daily Fixes", autosave: "History", ignore: "Vault",
};
const names: Record<string, string> = {
  "capture.inbox_note": "Inbox note", "capture.heading": "Capture heading",
  "templates.dir": "Templates folder", "templates.default_extension": "Default file extension",
  "attachments.dir": "Attachments folder", "attachments.max_file_bytes": "Maximum upload size",
  "autosave.min_interval": "Minimum time between history snapshots",
  "related.metadata_properties": "Properties used to find related notes",
  "brief.exclude_folders": "Excluded folders", "brief.exclude_types": "Excluded note types",
  "ignore": "Excluded paths", "cockpit.disabled_rules": "Disabled checks",
  "cockpit.priority": "Check priority", "cockpit.long_tail_limit": "Rare values to review per property",
  "cockpit.long_tail_max_count": "Maximum occurrences of a rare value",
  "cockpit.long_tail_head_factor": "Common-to-rare value ratio",
  "cockpit.long_tail_ignore_properties": "Properties excluded from rare-value checks",
  "cockpit.daily_limit": "Daily task limit", "cockpit.review_date_property": "Review date property",
  "cockpit.review_statuses": "Statuses requiring a review date", "cockpit.wip_statuses": "Work-in-progress statuses",
  "cockpit.max_wip": "Work-in-progress limit", "cockpit.stale_status_days": "Stale status age",
  "cockpit.max_captures": "Capture backlog limit", "cockpit.hub_type_values": "Hub note types",
  "cockpit.hub_empty_headings": "Hub headings to check", "cockpit.sensitive_scan": "Check for sensitive content",
  "cockpit.sensitive_ignore_paths": "Paths excluded from sensitive-content checks",
  "cockpit.sensitive_ignore_patterns": "Allowed sensitive-content patterns",
};

export const settingGroup = (key: string): string => groups[key.split(".")[0]] || "Other settings";
export const settingName = (key: string): string => names[key] || key.replaceAll(/[._]/g, " ");
export const settingUnit = (key: string): string => {
  if (key.endsWith("_bytes")) return "MiB";
  if (key.endsWith("_days")) return "days";
  return "";
};
export const settingDisplayValue = (summary: SettingSummary): string => {
  if (summary.kind === "bool") return summary.value.flag ? "On" : "Off";
  if (summary.key.endsWith("_bytes")) {
    const bytes = summary.value.number;
    return `${bytesAsMiB(bytes)} MiB`;
  }
  const unit = settingUnit(summary.key);
  return `${formatSettingValue(summary.value)}${unit ? ` ${unit}` : ""}`;
};
