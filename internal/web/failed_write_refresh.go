package web

import "net/http"

// failedWriteRecorder remembers the status a state-changing request was
// answered with, so the guard can tell a write that failed partway from one
// that succeeded or was refused.
type failedWriteRecorder struct {
	http.ResponseWriter
	status int
}

func (w *failedWriteRecorder) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *failedWriteRecorder) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *failedWriteRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// refreshAfterFailedWrite drops the cached note state once a state-changing
// request fails with a server error.
//
// A failed write is not proof that nothing landed: a Git snapshot failure
// arrives after the bytes are durable, and a rollback failure leaves part of the
// change on disk. Each handler that succeeds already invalidates what it
// changed; this covers every failure path at once, so no handler has to know
// which of its errors can follow a landed write. Dropping the snapshot only
// costs the next read a reload.
//
// Only a 500-class answer counts. Every error that can follow a landed write
// maps to one (noteops.HTTPStatusForError); a 4xx refusal and a 503 busy answer
// are both decided before anything is written, and they leave the cache alone.
func (s *Server) refreshAfterFailedWrite(w http.ResponseWriter, r *http.Request) (http.ResponseWriter, func()) {
	if s.state == nil || !stateChangingMethod(r.Method) {
		return w, func() {}
	}
	rec := &failedWriteRecorder{ResponseWriter: w}
	return rec, func() {
		if rec.status >= http.StatusInternalServerError && rec.status != http.StatusServiceUnavailable {
			s.state.invalidate()
		}
	}
}

func stateChangingMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}
