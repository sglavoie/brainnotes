package web

import (
	"net/http"
)

const (
	settingsRoutePath = "/settings"
	settingsPageTitle = "Settings - bn serve"
)

// settingsPageData seeds the two panels on the settings page.
//
// It carries endpoints and a token and nothing else: the settings table and the
// templates directory are both fetched by the browser from their own APIs, so
// the page and the API can never report different states — the same reason the
// properties page seeds only a shell.
type settingsPageData struct {
	Title             string
	CSRFToken         string
	SettingsEndpoint  string
	TemplatesEndpoint string
}

// handleSettingsPage renders the portable-configuration workspace.
//
// The page is deliberately a *write* surface and nothing else: it never lists a
// machine-local setting, because `/api/settings` does not serve one, and it
// never shows a template's bytes until one is opened by name. A read-only
// replica does not serve this page at all — replicaReadPath names neither it nor
// the two APIs it drives, so all three answer 403 and the nav link is gated — and
// `main.ts` does not build the controllers there either, so nothing rests on one
// of those two refusals alone.
func (s *Server) handleSettingsPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != settingsRoutePath {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	data := settingsPageData{
		Title:             settingsPageTitle,
		CSRFToken:         s.csrfToken,
		SettingsEndpoint:  settingsAPIRoutePath,
		TemplatesEndpoint: templatesAPIRoutePath,
	}
	if err := s.templates.ExecuteTemplate(w, "settings", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}
