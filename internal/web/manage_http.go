package web

import (
	"encoding/json"
	"net/http"
	"path"
	"path/filepath"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

func managementFormValue(r *http.Request, names ...string) (string, bool) {
	for _, name := range names {
		values, ok := r.Form[name]
		if !ok || len(values) == 0 {
			continue
		}
		return values[0], true
	}
	return "", false
}

func managementRouteKeyForRelPath(relPath string) string {
	relPath = strings.TrimSpace(filepath.ToSlash(relPath))
	ext := path.Ext(relPath)
	if strings.EqualFold(ext, ".md") {
		relPath = strings.TrimSuffix(relPath, ext)
	}
	relPath = strings.TrimPrefix(path.Clean("/"+relPath), "/")
	return strings.ToLower(relPath)
}

func mustManagementNotePath(relPath string) string {
	routePath, err := notePathForKey(managementRouteKeyForRelPath(relPath))
	if err != nil {
		return ""
	}
	return routePath
}

func frontmatterSchemaTypeFlag(schemaType string) string {
	switch strings.TrimSpace(schemaType) {
	case "string":
		return "raw"
	case "date", "int", "bool", "float":
		return schemaType
	case "list":
		return "raw"
	default:
		return ""
	}
}

func managementJournalID(entry *journal.Entry) string {
	if entry == nil {
		return ""
	}
	return entry.ID
}

func requestCSRFToken(r *http.Request) string {
	token := strings.TrimSpace(r.Header.Get(csrfHeaderName))
	if token == "" {
		token = strings.TrimSpace(r.Form.Get("token"))
	}
	return token
}

func writeManagementError(w http.ResponseWriter, err error) {
	writeManagementErrorStatus(w, noteops.HTTPStatusForError(err), err)
}

func writeManagementErrorStatus(w http.ResponseWriter, status int, err error) {
	if err == nil {
		err = noteops.Errorf(noteops.CodeInternal, "internal error")
	}
	writeManagementJSON(w, status, noteops.ErrorResponse(err))
}

func writeManagementJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
