package web

// Shared capture uses the same destination, token and tab-local draft identity
// as the Brief. Reading this context does not create an inbox or activity.
func (s *Server) captureContext() rootPageData {
	cfg := s.config()
	data := rootPageData{VaultID: s.vaultID, CSRFToken: s.csrfToken}
	data.CaptureRelPath, data.CaptureHeading, data.CaptureConfigured = captureDestination(cfg.Capture.InboxNote, cfg.Capture.Heading)
	return data
}

// configureCapturePage links only an inbox present in the served snapshot. A
// missing inbox is created by capture, whose response then supplies its URL.
func (s *Server) configureCapturePage(data *rootPageData, snapshot vaultSnapshot) {
	cfg := s.config()
	data.VaultID = s.vaultID
	data.CaptureRelPath, data.CaptureHeading, data.CaptureConfigured = captureDestination(cfg.Capture.InboxNote, cfg.Capture.Heading)
	if !data.CaptureConfigured {
		return
	}
	key := managementRouteKeyForRelPath(data.CaptureRelPath)
	if note, resolved, ok := noteForRoute(snapshot, key); ok && noteVaultPath(s.vault.Root, note) == data.CaptureRelPath {
		data.CapturePath, _ = notePathForKey(resolved)
	}
}
