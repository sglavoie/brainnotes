package web

import (
	"io/fs"
	"net/http"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/agent"
	"github.com/sglavoie/sglavoie_brainnotes/internal/brief"
)

func (srv *Server) routes(staticRoot fs.FS) http.Handler {
	mux := http.NewServeMux()
	staticHandler := http.StripPrefix("/static/", http.FileServer(http.FS(staticRoot)))
	mux.Handle("/static/", noCacheStatic(staticHandler))
	mux.HandleFunc(noteRoutePrefix, srv.handleNote)
	mux.HandleFunc("/broken", srv.handleBroken)
	mux.HandleFunc("/api/facets", srv.handleFacets)
	mux.HandleFunc("/api/ready", srv.handleReady)
	mux.HandleFunc(queryValidateAPIRoutePath, srv.handleQueryValidate)
	mux.HandleFunc(queryCompletionsAPIRoutePath, srv.handleQueryCompletions)
	mux.HandleFunc(searchAPIRoutePath, srv.handleSearch)
	mux.HandleFunc(openAPIRoutePath, srv.handleOpen)
	mux.HandleFunc(configOpenAPIRoutePath, srv.handleConfigOpen)
	mux.HandleFunc(briefViewAPIRoutePath, srv.handleBriefView)
	mux.HandleFunc(briefItemAPIRoutePath, srv.handleBriefItem)
	mux.HandleFunc(relatedAPIRoutePath, srv.handleRelated)
	mux.HandleFunc(relatedItemAPIRoutePath, srv.handleRelatedItem)
	mux.HandleFunc(noteInsightAPIRoutePath, srv.handleNoteInsight)
	srv.registerNoteMutationRoutes(mux)
	srv.registerSourceRoutes(mux)
	srv.registerAttachmentRoutes(mux)
	mux.HandleFunc("/api/graph.json", srv.handleGraphJSON)
	mux.HandleFunc("/api/cockpit", srv.handleCockpitQueue)
	mux.HandleFunc("/api/cockpit/item", srv.handleCockpitItem)
	mux.HandleFunc("/api/daily-review/context", srv.handleDailyReviewContext)
	mux.HandleFunc(dailyReviewAgentAPIRoutePath, srv.handleDailyReviewAgent)
	mux.HandleFunc("/api/fixes/preview", srv.handleFixesPreview)
	mux.HandleFunc("/api/fixes/apply", srv.handleFixesApply)
	mux.HandleFunc(propertiesAPIRoutePath, srv.handleProperties)
	mux.HandleFunc(propertiesDetailAPIRoutePath, srv.handlePropertyDetail)
	mux.HandleFunc(propertyValueAPIRoutePath, srv.handlePropertyValue)
	mux.HandleFunc(propertyPreviewAPIRoutePath, srv.handlePropertiesPreview)
	mux.HandleFunc(propertyApplyAPIRoutePath, srv.handlePropertiesApply)
	mux.HandleFunc(propertySchemaPreviewAPIRoutePath, srv.handlePropertySchemaPreview)
	mux.HandleFunc(propertySchemaApplyAPIRoutePath, srv.handlePropertySchemaApply)
	mux.HandleFunc(viewsAPIRoutePath, srv.handleViews)
	mux.HandleFunc(viewsPreviewAPIRoutePath, srv.handleViewsPreview)
	mux.HandleFunc(viewsApplyAPIRoutePath, srv.handleViewsApply)
	srv.registerSettingsRoutes(mux)
	srv.registerTemplateRoutes(mux)
	srv.registerSynthesisRoutes(mux)
	srv.registerSyncRoutes(mux)
	srv.registerHistoryRoutes(mux)
	srv.registerPageRoutes(mux)
	mux.HandleFunc(configStatusRoutePath, srv.handleConfigStatus)
	mux.HandleFunc(mediaRoutePrefix, srv.handleMedia)
	mux.HandleFunc("/", srv.handleIndex)

	return srv.requestGuard(mux)
}

// registerPageRoutes groups the routes that render a page, for the reason
// registerNoteMutationRoutes and registerSynthesisRoutes group theirs: routes is
// a flat list at the funlen ceiling, so a session adding a surface has to give
// statements back. The grouping is by kind — these all answer with HTML a person
// looks at — which is why /synthesize moved here from beside its APIs and why
// "/" and /static/ did not: the catch-all has to stay last in its own function
// for the reader to see that it is last.
func (srv *Server) registerPageRoutes(mux *http.ServeMux) {
	mux.HandleFunc(synthesizeRoutePath, srv.handleSynthesizePage)
	mux.HandleFunc(editorRoutePath, srv.handleEditorPage)
	mux.HandleFunc(newNoteRoutePath, srv.handleNewNotePage)
	mux.HandleFunc(propertiesRoutePath, srv.handlePropertiesPage)
	mux.HandleFunc(propertiesRoutePrefix, srv.handlePropertyPage)
	mux.HandleFunc(pinnedRoutePath, srv.handlePinnedPage)
	mux.HandleFunc(settingsRoutePath, srv.handleSettingsPage)
	mux.HandleFunc(libraryRoutePath, srv.handleLibrary)
	mux.HandleFunc(healthRoutePath, srv.handleHealth)
	mux.HandleFunc("/graph", srv.handleGraph)
}

func (srv *Server) configureRuntime(opts Options) error {
	srv.synthesisContexts.startSweeper(synthesisSweepInterval)
	srv.synthesisPreviews.startSweeper(synthesisSweepInterval)
	if srv.csrfToken == "" {
		token, err := generateCSRFToken()
		srv.csrfToken = token
		if err != nil {
			return err
		}
	}
	if srv.startEditor == nil {
		srv.startEditor = startEditorProcess
	}
	if srv.openBrowser == nil {
		srv.openBrowser = openBrowserDefault
	}
	srv.briefNow = opts.BriefNow
	if srv.briefNow == nil {
		srv.briefNow = time.Now
	}
	srv.briefState = opts.BriefState
	if srv.briefState == nil {
		srv.briefState = brief.NewFileStateStore(srv.vault.Root, brief.WithClock(srv.briefNow), brief.WithStateRoot(opts.StateDir))
	}
	if srv.runAgent == nil {
		srv.runAgent = agent.Run
	}

	return nil
}
