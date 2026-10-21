package web

import (
	"net/http"
	"strings"

	"github.com/sglavoie/brainnotes/internal/noteops"
)

type bulkManagementRequest struct {
	Action                     string
	SelectedKeys               []string
	Sources                    []noteops.BulkSource
	KeyFailures                []noteops.BulkFailure
	MoveFolder                 string
	FrontmatterProperty        string
	FrontmatterOp              string
	FrontmatterValue           string
	FrontmatterType            string
	FrontmatterMutations       []noteops.FrontmatterMutation
	FrontmatterUnsetProperties []string
}

type bulkManagementRequestFingerprint struct {
	Action              string
	SelectedKeys        []string
	Sources             []bulkManagementSourceFingerprint
	MoveFolder          string
	FrontmatterProperty string
	FrontmatterOp       string
	FrontmatterValue    string
	FrontmatterType     string
}

type bulkManagementSourceFingerprint struct {
	Key     string
	RelPath string
}

func (req bulkManagementRequest) fingerprint() bulkManagementRequestFingerprint {
	sources := make([]bulkManagementSourceFingerprint, 0, len(req.Sources))
	for _, source := range req.Sources {
		sources = append(sources, bulkManagementSourceFingerprint{
			Key:     source.Key,
			RelPath: source.RelPath,
		})
	}
	return bulkManagementRequestFingerprint{
		Action:              req.Action,
		SelectedKeys:        append([]string(nil), req.SelectedKeys...),
		Sources:             sources,
		MoveFolder:          req.MoveFolder,
		FrontmatterProperty: req.FrontmatterProperty,
		FrontmatterOp:       req.FrontmatterOp,
		FrontmatterValue:    req.FrontmatterValue,
		FrontmatterType:     req.FrontmatterType,
	}
}

func (s *Server) parseBulkManagementRequest(r *http.Request) (bulkManagementRequest, error) {
	var req bulkManagementRequest
	if err := r.ParseForm(); err != nil {
		return req, noteops.WrapError(noteops.CodeInvalidRequest, "invalid request form", err)
	}
	if !csrfTokenMatches(s.csrfToken, requestCSRFToken(r)) {
		return req, noteops.Errorf(noteops.CodeForbidden, "csrf token required")
	}

	action := strings.TrimSpace(r.Form.Get("action"))
	if action == "" {
		return req, noteops.Errorf(noteops.CodeInvalidRequest, "bulk: missing action")
	}
	req.Action = action

	keys := readBulkKeys(r.Form)
	if len(keys) == 0 {
		return req, noteops.Errorf(noteops.CodeInvalidRequest, "bulk: no notes selected")
	}
	req.SelectedKeys = keys

	sources, keyFailures, err := s.resolveBulkSources(keys)
	if err != nil {
		return req, err
	}
	req.Sources = sources
	req.KeyFailures = keyFailures
	canonicalAction, ok := canonicalBulkAction(action)
	if ok {
		req.Action = canonicalAction
	}
	if len(req.Sources) == 0 {
		return req, nil
	}
	if !ok {
		return req, noteops.Errorf(noteops.CodeInvalidRequest, "bulk: unsupported action %q", action)
	}

	if err := s.parseBulkActionFields(r, &req); err != nil {
		return req, err
	}
	return req, nil
}

func canonicalBulkAction(action string) (string, bool) {
	switch action {
	case "trash":
		return "trash", true
	case "move":
		return "move", true
	case "frontmatter_set", "frontmatter-set", "set_frontmatter":
		return "frontmatter_set", true
	case "frontmatter_unset", "frontmatter-unset", "unset_frontmatter":
		return "frontmatter_unset", true
	default:
		return "", false
	}
}

func (s *Server) parseBulkActionFields(r *http.Request, req *bulkManagementRequest) error {
	cfg := s.config()
	switch req.Action {
	case "trash":
		return nil
	case "move":
		newFolder, ok := managementFormValue(r, "folder", "new_folder")
		if !ok {
			return noteops.Errorf(noteops.CodeInvalidRequest, "move: missing new folder")
		}
		req.MoveFolder = newFolder
		return nil
	case "frontmatter_set":
		property, ok := managementFormValue(r, "property", "prop", "frontmatter_key")
		if !ok {
			return noteops.Errorf(noteops.CodeInvalidRequest, "frontmatter: missing property")
		}
		op := strings.TrimSpace(r.Form.Get("op"))
		if op == "" {
			op = "="
		}
		value, ok := managementFormValue(r, "value")
		if !ok {
			return noteops.Errorf(noteops.CodeInvalidRequest, "frontmatter: missing value")
		}
		typeFlag := strings.TrimSpace(r.Form.Get("type"))
		mutation, err := noteops.NewFrontmatterMutation(property, op, value, typeFlag, &cfg.Frontmatter)
		if err != nil {
			return err
		}
		req.FrontmatterProperty = property
		req.FrontmatterOp = op
		req.FrontmatterValue = value
		req.FrontmatterType = typeFlag
		req.FrontmatterMutations = []noteops.FrontmatterMutation{mutation}
		return nil
	case "frontmatter_unset":
		property, ok := managementFormValue(r, "property", "prop", "frontmatter_key")
		if !ok {
			return noteops.Errorf(noteops.CodeInvalidRequest, "frontmatter: missing property")
		}
		req.FrontmatterProperty = property
		req.FrontmatterUnsetProperties = []string{property}
		return nil
	default:
		return noteops.Errorf(noteops.CodeInvalidRequest, "bulk: unsupported action %q", req.Action)
	}
}

func readBulkKeys(form map[string][]string) []string {
	values := make([]string, 0)
	for _, name := range []string{"keys[]", "keys", "key"} {
		values = append(values, form[name]...)
	}

	keys := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		key := strings.TrimSpace(value)
		if key == "" {
			continue
		}
		folded := strings.ToLower(key)
		if _, ok := seen[folded]; ok {
			continue
		}
		seen[folded] = struct{}{}
		keys = append(keys, key)
	}
	return keys
}
