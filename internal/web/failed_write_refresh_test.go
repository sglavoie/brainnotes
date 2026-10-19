package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRefreshAfterFailedWriteDropsSnapshotOnlyForFailedWrites(t *testing.T) {
	cases := []struct {
		name    string
		method  string
		status  int
		dropped bool
	}{
		{"failed write", http.MethodPost, http.StatusInternalServerError, true},
		{"refused write", http.MethodPost, http.StatusConflict, false},
		{"busy write", http.MethodPost, http.StatusServiceUnavailable, false},
		{"successful write", http.MethodPost, http.StatusOK, false},
		{"failed read", http.MethodGet, http.StatusInternalServerError, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			loaded := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
			srv := &Server{state: &vaultState{snapshot: vaultSnapshot{loadedAt: loaded, serial: 7}}}
			r := httptest.NewRequest(tc.method, "/api/notes/trash", nil)
			w, settle := srv.refreshAfterFailedWrite(httptest.NewRecorder(), r)
			w.WriteHeader(tc.status)
			settle()
			if dropped := srv.state.snapshot.serial == 0; dropped != tc.dropped {
				t.Fatalf("snapshot dropped = %v, want %v", dropped, tc.dropped)
			}
		})
	}
}

func TestRefreshAfterFailedWriteKeepsRedactionReachable(t *testing.T) {
	srv := &Server{state: &vaultState{}}
	r := httptest.NewRequest(http.MethodPost, "/api/notes/trash", nil)
	w, settle := srv.refreshAfterFailedWrite(httptest.NewRecorder(), r)
	defer settle()
	if _, ok := w.(interface{ Unwrap() http.ResponseWriter }); !ok {
		t.Fatal("the recorder hides the underlying writer from http.ResponseController")
	}
}
