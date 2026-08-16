package web

import (
	"net/http"
)

const (
	propertiesPageTitle      = "Properties - bn serve"
	propertyDetailPageSuffix = " - Properties - bn serve"
)

// propertiesPageData seeds the catalog shell. The catalog itself is fetched by
// the browser from the properties API so the page and the API can never report
// different numbers.
type propertiesPageData struct {
	Title          string
	CSRFToken      string
	Endpoint       string
	DetailEndpoint string
}

// propertyDetailPageData seeds the drill-down shell for one property.
type propertyDetailPageData struct {
	Title          string
	CSRFToken      string
	Property       string
	PropertiesPath string
	Endpoint       string
	DetailEndpoint string
}

func (s *Server) handlePropertiesPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != propertiesRoutePath {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	data := propertiesPageData{
		Title:          propertiesPageTitle,
		CSRFToken:      s.csrfToken,
		Endpoint:       propertiesAPIRoutePath,
		DetailEndpoint: propertiesDetailAPIRoutePath,
	}
	if err := s.templates.ExecuteTemplate(w, "properties", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func (s *Server) handlePropertyPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path == propertiesRoutePrefix {
		http.Redirect(w, r, propertiesRoutePath, http.StatusMovedPermanently)
		return
	}
	name, ok := propertyNameFromPath(r.URL.EscapedPath())
	if !ok {
		http.NotFound(w, r)
		return
	}

	index, _, err := s.propertyIntelligence()
	if err != nil {
		http.Error(w, "properties error", http.StatusInternalServerError)
		return
	}
	if _, found := index.Property(name); !found {
		http.NotFound(w, r)
		return
	}

	data := propertyDetailPageData{
		Title:          name + propertyDetailPageSuffix,
		CSRFToken:      s.csrfToken,
		Property:       name,
		PropertiesPath: propertiesRoutePath,
		Endpoint:       propertiesAPIRoutePath,
		DetailEndpoint: propertiesDetailAPIRoutePath,
	}
	if err := s.templates.ExecuteTemplate(w, "propertyDetail", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}
