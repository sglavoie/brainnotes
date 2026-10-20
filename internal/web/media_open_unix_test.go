//go:build unix

package web

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// The media handler checks the path is a regular file by name before it opens
// it, so an entry swapped for a FIFO in between reaches openMediaFile. It must
// be refused on the opened descriptor, and the open must not wait for a writer.
func TestOpenMediaFileRefusesAFIFOWithoutBlocking(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "image.png")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}

	type result struct {
		ok  bool
		err error
	}
	done := make(chan result, 1)
	go func() {
		file, _, ok, err := openMediaFile(fifo)
		if file != nil {
			_ = file.Close()
		}
		done <- result{ok: ok, err: err}
	}()
	select {
	case got := <-done:
		if got.err != nil || got.ok {
			t.Fatalf("openMediaFile(FIFO) = ok %v, err %v; want a refusal", got.ok, got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("openMediaFile blocked on a FIFO with no writer")
	}
}

// The non-blocking open must not change how a regular file is served.
func TestOpenMediaFileServesARegularFile(t *testing.T) {
	regular := filepath.Join(t.TempDir(), "image.png")
	if err := os.WriteFile(regular, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	file, info, ok, err := openMediaFile(regular)
	if err != nil || !ok {
		t.Fatalf("openMediaFile(regular) = ok %v, err %v", ok, err)
	}
	defer file.Close()
	if info.Size() != 3 {
		t.Fatalf("size = %d, want 3", info.Size())
	}
}
