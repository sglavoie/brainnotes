package noteops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
)

// TestApplyCaptureConcurrentAppends races captures against one inbox that does
// not exist yet, so the run covers both the bootstrap precondition (exactly one
// creator wins) and the append precondition (no writer overwrites another's
// bullet).
func TestApplyCaptureConcurrentAppends(t *testing.T) {
	t.Parallel()

	const captures = 8
	root := t.TempDir()

	var wg sync.WaitGroup
	errs := make(chan error, captures)
	for i := 0; i < captures; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := captureRequest(fmt.Sprintf("capture %d", i), 30)
			if err := captureUntilApplied(root, req); err != nil {
				errs <- fmt.Errorf("capture %d: %w", i, err)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("ApplyCapture: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, "Inbox.md"))
	if err != nil {
		t.Fatalf("read inbox: %v", err)
	}
	doc, err := frontmatter.Parse(data)
	if err != nil {
		t.Fatalf("inbox does not parse: %v\n%s", err, data)
	}
	if title, _ := doc.Front.GetString("title"); title != "Inbox" {
		t.Fatalf("title = %q, want Inbox", title)
	}
	if got := strings.Count(doc.Body, captureHeading+"\n"); got != 1 {
		t.Fatalf("capture heading appears %d times, want 1:\n%s", got, doc.Body)
	}
	if got := CountSectionBullets(doc.Body, captureHeading); got != captures {
		t.Fatalf("section bullets = %d, want %d:\n%s", got, captures, doc.Body)
	}
	for i := 0; i < captures; i++ {
		want := fmt.Sprintf("- 2026-08-17 09:30 capture %d\n", i)
		if got := strings.Count(doc.Body, want); got != 1 {
			t.Fatalf("bullet %q appears %d times, want 1:\n%s", want, got, doc.Body)
		}
	}
}

// captureUntilApplied retries the conflict ApplyCapture reports once its three
// internal replans have all lost the race. Contention that heavy is not what
// this test is pinning, and a caller facing it is expected to try again.
func captureUntilApplied(root string, req CaptureRequest) error {
	deadline := time.Now().Add(30 * time.Second)
	for {
		_, err := ApplyCapture(root, "Inbox.md", req, nil)
		if err == nil || CodeOf(err) != CodeConflict || time.Now().After(deadline) {
			return err
		}
		time.Sleep(time.Millisecond)
	}
}
