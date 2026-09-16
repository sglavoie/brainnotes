package web

import (
	"net/http"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

const configStatusRoutePath = "/api/config/status"

type configStatusResponse struct {
	OK bool `json:"ok"`
	ConfigStatus
}

// handleConfigStatus is the lightweight polling path for configuration reloads.
// It re-reads both configuration layers, publishes them when they are valid and
// the composite fingerprint changed, and always reports the resulting status so
// a client can surface an invalid file without waiting for the next write to
// fail. An edit confined to brainnotes.local.toml — including its appearance or
// removal — moves the generation exactly as a portable edit does, which is what
// makes an overlay change invalidate outstanding receipts.
func (s *Server) handleConfigStatus(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != configStatusRoutePath {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeManagementErrorStatus(w, http.StatusMethodNotAllowed, noteops.Errorf(noteops.CodeInvalidRequest, "method not allowed"))
		return
	}
	if err := s.reloadConfig(); err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "config: reload failed", err))
		return
	}
	status := s.configState.Status()
	status.Writable = status.Writable && s.caps.documentWrites
	status.Mode = string(s.caps.mode)
	status.ProcessLaunch = s.caps.processLaunch
	writeManagementJSON(w, http.StatusOK, configStatusResponse{
		OK:           true,
		ConfigStatus: status,
	})
}
