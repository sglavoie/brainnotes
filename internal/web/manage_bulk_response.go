package web

import (
	"fmt"
	"net/http"
	"time"

	"github.com/sglavoie/brainnotes/internal/noteops"
)

type bulkManagementResponse struct {
	OK        bool                  `json:"ok"`
	Code      string                `json:"code,omitempty"`
	Message   string                `json:"message"`
	Changed   int                   `json:"changed"`
	Unchanged int                   `json:"unchanged,omitempty"`
	Failed    int                   `json:"failed"`
	JournalID string                `json:"journal_id,omitempty"`
	Results   []bulkManagementItem  `json:"results,omitempty"`
	Failures  []noteops.BulkFailure `json:"failures,omitempty"`
}

type bulkManagementItem struct {
	Key     string `json:"key"`
	Path    string `json:"path"`
	RelPath string `json:"rel_path"`
	Changed bool   `json:"changed"`
}

type bulkPreviewManagementResponse struct {
	OK        bool                        `json:"ok"`
	Code      string                      `json:"code,omitempty"`
	Action    string                      `json:"action"`
	Message   string                      `json:"message"`
	Changed   int                         `json:"changed"`
	Unchanged int                         `json:"unchanged"`
	Failed    int                         `json:"failed"`
	PreviewID string                      `json:"preview_id"`
	ExpiresAt string                      `json:"expires_at"`
	Results   []bulkPreviewManagementItem `json:"results,omitempty"`
	Failures  []noteops.BulkFailure       `json:"failures,omitempty"`
}

type bulkPreviewManagementItem struct {
	Key         string                           `json:"key"`
	Path        string                           `json:"path"`
	RelPath     string                           `json:"rel_path"`
	Source      bulkManagementPath               `json:"source"`
	Destination *bulkManagementPath              `json:"destination,omitempty"`
	Changed     bool                             `json:"changed"`
	Move        *noteops.BulkMovePreview         `json:"move,omitempty"`
	Frontmatter []noteops.BulkFrontmatterPreview `json:"frontmatter,omitempty"`
	Affected    []bulkManagementAffectedPath     `json:"affected,omitempty"`
}

type bulkManagementPath struct {
	Key     string `json:"key"`
	Path    string `json:"path"`
	RelPath string `json:"rel_path"`
}

type bulkManagementAffectedPath struct {
	Role    string `json:"role"`
	Key     string `json:"key"`
	Path    string `json:"path"`
	RelPath string `json:"rel_path"`
	Exists  bool   `json:"exists"`
}

func bulkManagementResponseForResult(action string, result *noteops.BulkResult) bulkManagementResponse {
	if result == nil {
		return bulkManagementResponse{
			OK:      false,
			Code:    string(noteops.CodeInternal),
			Message: "Bulk action failed.",
		}
	}
	return bulkManagementResponse{
		OK:        true,
		Message:   bulkManagementMessage(action, result),
		Changed:   result.Changed,
		Unchanged: result.Unchanged,
		Failed:    len(result.Failures),
		JournalID: managementJournalID(result.Journal),
		Results:   bulkManagementItems(result),
		Failures:  result.Failures,
	}
}

func bulkManagementItems(result *noteops.BulkResult) []bulkManagementItem {
	items := make([]bulkManagementItem, 0, len(result.Results))
	for _, item := range result.Results {
		relPath := item.Source.RelPath
		if item.Destination.RelPath != "" {
			relPath = item.Destination.RelPath
		}
		key := managementRouteKeyForRelPath(relPath)
		items = append(items, bulkManagementItem{
			Key:     key,
			Path:    mustManagementNotePath(relPath),
			RelPath: relPath,
			Changed: item.Changed,
		})
	}
	return items
}

func bulkPreviewManagementResponseForResult(action string, result *noteops.BulkPreviewResult, receipt bulkPreviewReceipt) bulkPreviewManagementResponse {
	if result == nil {
		return bulkPreviewManagementResponse{
			OK:      false,
			Code:    string(noteops.CodeInternal),
			Action:  action,
			Message: "Bulk preview failed.",
		}
	}
	return bulkPreviewManagementResponse{
		OK:        true,
		Action:    action,
		Message:   bulkPreviewManagementMessage(action, result),
		Changed:   result.Changed,
		Unchanged: result.Unchanged,
		Failed:    len(result.Failures),
		PreviewID: receipt.ID,
		ExpiresAt: receipt.ExpiresAt.UTC().Format(time.RFC3339),
		Results:   bulkPreviewManagementItems(result),
		Failures:  result.Failures,
	}
}

func bulkPreviewManagementItems(result *noteops.BulkPreviewResult) []bulkPreviewManagementItem {
	items := make([]bulkPreviewManagementItem, 0, len(result.Results))
	for _, item := range result.Results {
		source := bulkManagementPathForResolution(item.Source)
		relPath := item.Source.RelPath
		path := source.Path

		var destination *bulkManagementPath
		if item.Destination.RelPath != "" {
			dest := bulkManagementPathForResolution(item.Destination)
			destination = &dest
			relPath = dest.RelPath
			path = dest.Path
		}

		items = append(items, bulkPreviewManagementItem{
			Key:         item.Key,
			Path:        path,
			RelPath:     relPath,
			Source:      source,
			Destination: destination,
			Changed:     item.Changed,
			Move:        item.Move,
			Frontmatter: item.Frontmatter,
			Affected:    bulkManagementAffectedPaths(item.Affected),
		})
	}
	return items
}

func bulkManagementAffectedPaths(paths []noteops.BulkAffectedPath) []bulkManagementAffectedPath {
	out := make([]bulkManagementAffectedPath, 0, len(paths))
	for _, p := range paths {
		resolved := bulkManagementPathForResolution(p.Path)
		out = append(out, bulkManagementAffectedPath{
			Role:    p.Role,
			Key:     resolved.Key,
			Path:    resolved.Path,
			RelPath: resolved.RelPath,
			Exists:  p.Exists,
		})
	}
	return out
}

func bulkManagementPathForResolution(resolved noteops.PathResolution) bulkManagementPath {
	key := managementRouteKeyForRelPath(resolved.RelPath)
	return bulkManagementPath{
		Key:     key,
		Path:    mustManagementNotePath(resolved.RelPath),
		RelPath: resolved.RelPath,
	}
}

func bulkPreviewManagementMessage(action string, result *noteops.BulkPreviewResult) string {
	changed := result.Changed
	failures := len(result.Failures)
	var message string
	switch action {
	case "trash":
		message = fmt.Sprintf("Preview: %s would be moved to Trash.", noteCountLabel(changed))
	case "move":
		message = fmt.Sprintf("Preview: %s would be moved.", noteCountLabel(changed))
	case "frontmatter_set":
		if changed == 0 {
			message = "Preview: frontmatter would be unchanged on selected notes."
		} else {
			message = fmt.Sprintf("Preview: frontmatter would be updated on %s.", noteCountLabel(changed))
		}
	case "frontmatter_unset":
		if changed == 0 {
			message = "Preview: frontmatter would be unchanged on selected notes."
		} else {
			message = fmt.Sprintf("Preview: frontmatter would be unset on %s.", noteCountLabel(changed))
		}
	default:
		message = fmt.Sprintf("Preview: %s would be updated.", noteCountLabel(changed))
	}
	if failures > 0 {
		message += fmt.Sprintf(" %d failed.", failures)
	}
	return message
}

func bulkManagementMessage(action string, result *noteops.BulkResult) string {
	changed := result.Changed
	failures := len(result.Failures)
	var message string
	switch action {
	case "trash":
		message = fmt.Sprintf("Moved %s to Trash.", noteCountLabel(changed))
	case "move":
		message = fmt.Sprintf("Moved %s.", noteCountLabel(changed))
	case "frontmatter_set", "frontmatter-set", "set_frontmatter":
		if changed == 0 {
			message = "Frontmatter unchanged on selected notes."
		} else {
			message = fmt.Sprintf("Updated frontmatter on %s.", noteCountLabel(changed))
		}
	case "frontmatter_unset", "frontmatter-unset", "unset_frontmatter":
		if changed == 0 {
			message = "Frontmatter unchanged on selected notes."
		} else {
			message = fmt.Sprintf("Unset frontmatter on %s.", noteCountLabel(changed))
		}
	default:
		message = fmt.Sprintf("Updated %s.", noteCountLabel(changed))
	}
	if failures > 0 {
		message += fmt.Sprintf(" %d failed.", failures)
	}
	return message
}

func noteCountLabel(count int) string {
	if count == 1 {
		return "1 note"
	}
	return fmt.Sprintf("%d notes", count)
}

func bulkFailureCode(failures []noteops.BulkFailure) noteops.ErrorCode {
	if len(failures) == 0 || failures[0].Code == "" {
		return noteops.CodeInvalidRequest
	}
	return failures[0].Code
}

func bulkFailureStatus(failures []noteops.BulkFailure) int {
	switch bulkFailureCode(failures) {
	case noteops.CodeInvalidRequest, noteops.CodeInvalidKey:
		return http.StatusBadRequest
	case noteops.CodeForbidden:
		return http.StatusForbidden
	case noteops.CodeNotFound:
		return http.StatusNotFound
	case noteops.CodeConflict:
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}
