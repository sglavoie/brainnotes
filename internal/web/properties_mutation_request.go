package web

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/properties"
)

const (
	propertyValueAPIRoutePath         = "/api/properties/value"
	propertyPreviewAPIRoutePath       = "/api/properties/preview"
	propertyApplyAPIRoutePath         = "/api/properties/apply"
	propertySchemaPreviewAPIRoutePath = "/api/properties/schema/preview"
	propertySchemaApplyAPIRoutePath   = "/api/properties/schema/apply"

	// propertyRequestBodyLimit bounds a mutation request body. A property edit
	// carries a handful of values and a note selection, never a document.
	propertyRequestBodyLimit = 1 << 20
)

// propertyActionKind names one note-mutation action the workbench can request.
type propertyActionKind string

const (
	propertyActionSet          propertyActionKind = "set"
	propertyActionUnset        propertyActionKind = "unset"
	propertyActionReplaceValue propertyActionKind = "replace_value"
	propertyActionRename       propertyActionKind = "rename_property"
	propertyActionRepair       propertyActionKind = "repair"
)

// propertyValueOp names one immediate single-note edit. Both are safe: a
// replacement of a known value and an addition to a list.
type propertyValueOp string

const (
	propertyValueOpReplace propertyValueOp = "replace"
	propertyValueOpListAdd propertyValueOp = "list_add"
)

// propertySchemaOp names one definition-only change to brainnotes.toml.
// Renaming is deliberately absent: a rename also touches notes, so it is a
// note-mutation action and there is exactly one way to request it.
type propertySchemaOp string

const (
	propertySchemaOpUpsert propertySchemaOp = "upsert"
	propertySchemaOpDelete propertySchemaOp = "delete"
)

// propertyMutationRequest is a multi-note property change. It is always
// previewed before it can be applied.
type propertyMutationRequest struct {
	Token    string                 `json:"token"`
	Action   propertyActionKind     `json:"action"`
	Property string                 `json:"property"`
	NewName  string                 `json:"new_name"`
	Notes    []string               `json:"notes"`
	Value    *properties.TypedValue `json:"value"`
	Match    *properties.TypedValue `json:"match"`
}

// propertyApplyRequest redeems a preview receipt. It carries no plan of its
// own, so an apply can never describe work the preview did not show.
type propertyApplyRequest struct {
	Token       string `json:"token"`
	PreviewID   string `json:"preview_id"`
	OperationID string `json:"operation_id,omitempty"`
}

// propertyValueRequest is one immediate single-note edit, guarded by the note
// hash the browser rendered.
type propertyValueRequest struct {
	Token        string                 `json:"token"`
	OperationID  string                 `json:"operation_id,omitempty"`
	Note         string                 `json:"note"`
	Property     string                 `json:"property"`
	Op           propertyValueOp        `json:"op"`
	ExpectedHash string                 `json:"expected_hash"`
	Value        *properties.TypedValue `json:"value"`
}

// propertySchemaRequest is one definition change to brainnotes.toml.
type propertySchemaRequest struct {
	Token      string                    `json:"token"`
	Op         propertySchemaOp          `json:"op"`
	Property   string                    `json:"property"`
	Definition *propertySchemaDefinition `json:"definition"`
}

// propertySchemaDefinition is the transport form of a property definition.
type propertySchemaDefinition struct {
	Type     config.PropertyKind `json:"type"`
	Items    config.PropertyKind `json:"items"`
	Required bool                `json:"required"`
	Allowed  []string            `json:"allowed"`
}

func (d propertySchemaDefinition) definition() config.PropertyDefinition {
	return config.PropertyDefinition{
		Type:     d.Type,
		Items:    d.Items,
		Required: d.Required,
		Allowed:  append([]string(nil), d.Allowed...),
	}
}

// decodePropertyRequest reads a bounded JSON body into dst.
func decodePropertyRequest(r *http.Request, dst any) error {
	if r.Body == nil {
		return noteops.Errorf(noteops.CodeInvalidRequest, "properties: request body is required")
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, propertyRequestBodyLimit))
	if err := decoder.Decode(dst); err != nil {
		return noteops.WrapError(noteops.CodeInvalidRequest, "properties: invalid request body", err)
	}
	return nil
}

// checkPropertyCSRF accepts the token from the request header, falling back to
// the token carried in the JSON body for clients that cannot set headers.
func (s *Server) checkPropertyCSRF(r *http.Request, bodyToken string) error {
	token := strings.TrimSpace(r.Header.Get(csrfHeaderName))
	if token == "" {
		token = strings.TrimSpace(bodyToken)
	}
	if !csrfTokenMatches(s.csrfToken, token) {
		return noteops.Errorf(noteops.CodeForbidden, "csrf token required")
	}
	return nil
}

// requirePropertyPost rejects anything but a POST and advertises the allowed
// method, so a mistaken GET cannot reach a write path.
func requirePropertyPost(w http.ResponseWriter, r *http.Request, route string) bool {
	if r.URL.Path != route {
		http.NotFound(w, r)
		return false
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeManagementErrorStatus(w, http.StatusMethodNotAllowed, noteops.Errorf(noteops.CodeInvalidRequest, "method not allowed"))
		return false
	}
	return true
}

func (s *Server) parsePropertyMutationRequest(r *http.Request) (propertyMutationRequest, error) {
	var req propertyMutationRequest
	if err := decodePropertyRequest(r, &req); err != nil {
		return propertyMutationRequest{}, err
	}
	if err := s.checkPropertyCSRF(r, req.Token); err != nil {
		return propertyMutationRequest{}, err
	}
	return req.normalized()
}

func (req propertyMutationRequest) normalized() (propertyMutationRequest, error) {
	out := req
	out.Token = ""
	out.Property = strings.TrimSpace(req.Property)
	out.NewName = strings.TrimSpace(req.NewName)
	out.Notes = normalizePropertyNoteSelection(req.Notes)

	if out.Property == "" {
		return propertyMutationRequest{}, noteops.Errorf(noteops.CodeInvalidRequest, "properties: property is required")
	}
	if len(out.Notes) == 0 {
		return propertyMutationRequest{}, noteops.Errorf(noteops.CodeInvalidRequest, "properties: no notes selected")
	}

	switch out.Action {
	case propertyActionSet:
		if out.Value == nil {
			return propertyMutationRequest{}, noteops.Errorf(noteops.CodeInvalidRequest, "properties: set requires a value")
		}
	case propertyActionUnset, propertyActionRepair:
	case propertyActionReplaceValue:
		if out.Match == nil || out.Value == nil {
			return propertyMutationRequest{}, noteops.Errorf(noteops.CodeInvalidRequest, "properties: replace_value requires match and value")
		}
	case propertyActionRename:
		if out.NewName == "" {
			return propertyMutationRequest{}, noteops.Errorf(noteops.CodeInvalidRequest, "properties: rename_property requires new_name")
		}
		if out.NewName == out.Property {
			return propertyMutationRequest{}, noteops.Errorf(noteops.CodeInvalidRequest, "properties: rename_property requires a different name")
		}
	default:
		return propertyMutationRequest{}, noteops.Errorf(noteops.CodeInvalidRequest, "properties: unsupported action %q", string(out.Action))
	}
	return out, nil
}

func (s *Server) parsePropertyApplyRequest(r *http.Request) (propertyApplyRequest, error) {
	var req propertyApplyRequest
	if err := decodePropertyRequest(r, &req); err != nil {
		return propertyApplyRequest{}, err
	}
	if err := s.checkPropertyCSRF(r, req.Token); err != nil {
		return propertyApplyRequest{}, err
	}
	req.Token = ""
	req.PreviewID = strings.TrimSpace(req.PreviewID)
	if req.OperationID != "" && !validOperationID(req.OperationID) {
		return propertyApplyRequest{}, noteops.Errorf(noteops.CodeInvalidRequest, "invalid operation ID")
	}
	if req.PreviewID == "" {
		return propertyApplyRequest{}, noteops.Errorf(noteops.CodePreviewRequired, "properties: preview required")
	}
	return req, nil
}

func (s *Server) parsePropertyValueRequest(r *http.Request) (propertyValueRequest, error) {
	var req propertyValueRequest
	if err := decodePropertyRequest(r, &req); err != nil {
		return propertyValueRequest{}, err
	}
	if err := s.checkPropertyCSRF(r, req.Token); err != nil {
		return propertyValueRequest{}, err
	}
	req.Token = ""
	req.Note = normalizePropertyNotePath(req.Note)
	req.Property = strings.TrimSpace(req.Property)
	req.ExpectedHash = strings.TrimSpace(req.ExpectedHash)
	if err := validatePropertyValueRequest(req); err != nil {
		return propertyValueRequest{}, err
	}
	return req, nil
}

func validatePropertyValueRequest(req propertyValueRequest) error {
	if req.OperationID != "" && !validOperationID(req.OperationID) {
		return noteops.Errorf(noteops.CodeInvalidRequest, "invalid operation ID")
	}

	if req.Note == "" {
		return noteops.Errorf(noteops.CodeInvalidRequest, "properties: note is required")
	}
	if req.Property == "" {
		return noteops.Errorf(noteops.CodeInvalidRequest, "properties: property is required")
	}
	if req.ExpectedHash == "" {
		return noteops.Errorf(noteops.CodeInvalidRequest, "properties: expected_hash is required")
	}
	if req.Value == nil {
		return noteops.Errorf(noteops.CodeInvalidRequest, "properties: value is required")
	}
	switch req.Op {
	case propertyValueOpReplace, propertyValueOpListAdd:
	default:
		return noteops.Errorf(noteops.CodeInvalidRequest, "properties: unsupported op %q", string(req.Op))
	}
	return nil
}

func (s *Server) parsePropertySchemaRequest(r *http.Request) (propertySchemaRequest, error) {
	var req propertySchemaRequest
	if err := decodePropertyRequest(r, &req); err != nil {
		return propertySchemaRequest{}, err
	}
	if err := s.checkPropertyCSRF(r, req.Token); err != nil {
		return propertySchemaRequest{}, err
	}
	req.Token = ""
	req.Property = strings.TrimSpace(req.Property)
	if req.Property == "" {
		return propertySchemaRequest{}, noteops.Errorf(noteops.CodeInvalidRequest, "properties: property is required")
	}
	switch req.Op {
	case propertySchemaOpUpsert:
		if req.Definition == nil {
			return propertySchemaRequest{}, noteops.Errorf(noteops.CodeInvalidRequest, "properties: upsert requires a definition")
		}
		normalized := *req.Definition
		normalized.Allowed = normalizePropertyAllowed(normalized.Allowed)
		req.Definition = &normalized
	case propertySchemaOpDelete:
		req.Definition = nil
	default:
		return propertySchemaRequest{}, noteops.Errorf(noteops.CodeInvalidRequest, "properties: unsupported op %q", string(req.Op))
	}
	return req, nil
}

// normalizePropertyNoteSelection cleans, deduplicates, and sorts the selection
// so a preview and its apply describe the same notes in the same order no
// matter how the browser ordered them.
func normalizePropertyNoteSelection(notes []string) []string {
	out := make([]string, 0, len(notes))
	seen := make(map[string]struct{}, len(notes))
	for _, note := range notes {
		relPath := normalizePropertyNotePath(note)
		if relPath == "" {
			continue
		}
		if _, ok := seen[relPath]; ok {
			continue
		}
		seen[relPath] = struct{}{}
		out = append(out, relPath)
	}
	sort.Strings(out)
	return out
}

// normalizePropertyNotePath renders a requested note as a clean, slash-form
// vault-relative path. An absolute or parent-escaping path is rejected here by
// returning the empty string; containment is verified again when the path is
// resolved against the snapshot.
func normalizePropertyNotePath(note string) string {
	trimmed := strings.TrimSpace(filepath.ToSlash(note))
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "/") {
		return ""
	}
	cleaned := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(trimmed)), "./")
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return ""
	}
	return cleaned
}

func normalizePropertyAllowed(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}
