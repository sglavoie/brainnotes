package web

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/recall"
)

func TestServer_RelatedItemAPI_ConcurrentWritesPreserveEveryPair(t *testing.T) {
	candidates := []string{
		"Alpha", "Bravo", "Charlie", "Delta", "Echo", "Foxtrot",
		"Golf", "Hotel", "India", "Juliet", "Kilo", "Lima",
	}
	files := map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nsource\n"),
	}
	for _, candidate := range candidates {
		files[candidate+".md"] = []byte("---\ntitle: " + candidate + "\n---\ncandidate\n")
	}

	arrived := make(chan struct{}, len(candidates))
	release := make(chan struct{})
	now := time.Date(2026, time.August, 19, 11, 0, 0, 0, time.UTC)
	ts, root := newHTTPServerForFilesWithOptions(t, files, func(cfg *config.Config) {
		cfg.Open.Editor = "echo --goto"
	}, Options{
		CSRFToken: relatedItemTestToken,
		BriefNow:  func() time.Time { return now },
		StartEditor: func(string, []string) error {
			arrived <- struct{}{}
			<-release
			return nil
		},
	})

	type requestResult struct {
		candidate string
		err       error
	}
	results := make(chan requestResult, len(candidates))
	for _, candidate := range candidates {
		candidate := candidate
		go func() {
			values := relatedItemValues("source", strings.ToLower(candidate), "open_editor", "")
			req, err := http.NewRequest(http.MethodPost, ts.URL+relatedItemAPIRoutePath, strings.NewReader(values.Encode()))
			if err == nil {
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.Header.Set(csrfHeaderName, relatedItemTestToken)
				var response *http.Response
				response, err = http.DefaultClient.Do(req)
				if err == nil {
					body, readErr := io.ReadAll(response.Body)
					_ = response.Body.Close()
					if readErr != nil {
						err = readErr
					} else if response.StatusCode != http.StatusOK {
						err = fmt.Errorf("status %d: %s", response.StatusCode, body)
					}
				}
			}
			results <- requestResult{candidate: candidate, err: err}
		}()
	}

	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for range candidates {
		select {
		case <-arrived:
		case result := <-results:
			close(release)
			t.Fatalf("%s request completed before the concurrency barrier: %v", result.candidate, result.err)
		case <-timer.C:
			close(release)
			t.Fatal("timed out waiting for concurrent related-item requests")
		}
	}
	close(release)

	var firstErr error
	for range candidates {
		result := <-results
		if result.err != nil && firstErr == nil {
			firstErr = fmt.Errorf("%s request: %w", result.candidate, result.err)
		}
	}
	if firstErr != nil {
		t.Fatal(firstErr)
	}

	store := recall.NewStateStore(root, recall.WithClock(func() time.Time { return now }))
	for _, candidate := range candidates {
		signal, err := store.Signal("Source.md", candidate+".md")
		if err != nil || signal.LastOpenedAt.IsZero() {
			t.Errorf("final recall.json pair Source.md -> %s.md = %#v, err=%v", candidate, signal, err)
		}
	}
}

func TestServer_RelatedItemAPI_EditorOrderingFailuresAndPartialSuccess(t *testing.T) {
	files := map[string][]byte{
		"Source.md":    []byte("---\ntitle: Source\n---\nsource [[Candidate]]\n"),
		"Candidate.md": []byte("---\ntitle: Candidate\n---\ncandidate\n"),
	}
	now := time.Date(2026, time.August, 19, 10, 0, 0, 0, time.UTC)

	t.Run("success preserves feedback", func(t *testing.T) {
		started := 0
		ts, root := newHTTPServerForFilesWithOptions(t, files, func(cfg *config.Config) {
			cfg.Open.Editor = "echo --goto"
		}, Options{
			CSRFToken: relatedItemTestToken,
			BriefNow:  func() time.Time { return now },
			StartEditor: func(string, []string) error {
				started++
				return nil
			},
		})
		postRelatedItemOK(t, ts.URL, relatedItemValues("source", "candidate", "useful", ""))
		payload := postRelatedItemOK(t, ts.URL, relatedItemValues("source", "candidate", "open_editor", ""))
		if started != 1 || !payload.Opened || payload.Recorded == nil || !*payload.Recorded || payload.Warning != "" {
			t.Fatalf("started/payload = %d/%#v", started, payload)
		}
		signal, err := recall.NewStateStore(root).Signal("Source.md", "Candidate.md")
		if err != nil || !signal.Useful || signal.LastOpenedAt.IsZero() {
			t.Fatalf("signal = %#v, err=%v", signal, err)
		}
	})

	t.Run("start failure does not record", func(t *testing.T) {
		ts, root := newHTTPServerForFilesWithOptions(t, files, func(cfg *config.Config) {
			cfg.Open.Editor = "echo --goto"
		}, Options{
			CSRFToken: relatedItemTestToken,
			BriefNow:  func() time.Time { return now },
			StartEditor: func(string, []string) error {
				return errors.New("boom")
			},
		})
		resp := postRelatedItemRequest(t, ts.URL, relatedItemValues("source", "candidate", "open_editor", ""), relatedItemTestToken)
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusInternalServerError || !strings.Contains(body, "editor start error") {
			t.Fatalf("status/body = %d/%s", resp.StatusCode, body)
		}
		signal, err := recall.NewStateStore(root).Signal("Source.md", "Candidate.md")
		if err != nil || !signal.LastOpenedAt.IsZero() {
			t.Fatalf("signal = %#v, err=%v", signal, err)
		}
	})

	t.Run("editor unavailable does not record", func(t *testing.T) {
		t.Setenv("EDITOR", "")
		ts, root := newHTTPServerForFilesWithOptions(t, files, func(cfg *config.Config) {
			cfg.Open.Editor = ""
		}, Options{CSRFToken: relatedItemTestToken, BriefNow: func() time.Time { return now }})
		resp := postRelatedItemRequest(t, ts.URL, relatedItemValues("source", "candidate", "open_editor", ""), relatedItemTestToken)
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusPreconditionFailed || !strings.Contains(body, "editor not configured") {
			t.Fatalf("status/body = %d/%s", resp.StatusCode, body)
		}
		signal, err := recall.NewStateStore(root).Signal("Source.md", "Candidate.md")
		if err != nil || !signal.LastOpenedAt.IsZero() {
			t.Fatalf("signal = %#v, err=%v", signal, err)
		}
	})

	t.Run("state validation precedes editor", func(t *testing.T) {
		started := 0
		ts, root := newHTTPServerForFilesWithOptions(t, files, func(cfg *config.Config) {
			cfg.Open.Editor = "echo --goto"
		}, Options{
			CSRFToken: relatedItemTestToken,
			BriefNow:  func() time.Time { return now },
			StartEditor: func(string, []string) error {
				started++
				return nil
			},
		})
		statePath := filepath.Join(root, ".bnotes", "recall.json")
		if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(statePath, []byte("{broken\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		resp := postRelatedItemRequest(t, ts.URL, relatedItemValues("source", "candidate", "open_editor", ""), relatedItemTestToken)
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusConflict || started != 0 || !strings.Contains(body, "recall state is unreadable") {
			t.Fatalf("status/started/body = %d/%d/%s", resp.StatusCode, started, body)
		}
	})

	t.Run("editor success and recall write failure", func(t *testing.T) {
		var root string
		external := filepath.Join(t.TempDir(), "external-recall.json")
		if err := os.WriteFile(external, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		ts, gotRoot := newHTTPServerForFilesWithOptions(t, files, func(cfg *config.Config) {
			cfg.Open.Editor = "echo --goto"
		}, Options{
			CSRFToken: relatedItemTestToken,
			BriefNow:  func() time.Time { return now },
			StartEditor: func(string, []string) error {
				statePath := filepath.Join(root, ".bnotes", "recall.json")
				if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
					return err
				}
				return os.Symlink(external, statePath)
			},
		})
		root = gotRoot
		payload := postRelatedItemOK(t, ts.URL, relatedItemValues("source", "candidate", "open_editor", ""))
		if !payload.Opened || payload.Recorded == nil || *payload.Recorded || payload.Warning == "" {
			t.Fatalf("partial payload = %#v", payload)
		}
		if info, err := os.Lstat(filepath.Join(root, ".bnotes", "recall.json")); err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("state sidecar = %v, err=%v", info, err)
		}
	})
}

func TestServer_RelatedItemAPI_EditorRejectsCandidateReplacedByEscapingSymlink(t *testing.T) {
	files := map[string][]byte{
		"Source.md":    []byte("---\ntitle: Source\n---\nsource [[Candidate]]\n"),
		"Candidate.md": []byte("---\ntitle: Candidate\n---\ncandidate\n"),
	}
	started := 0
	ts, root := newHTTPServerForFilesWithOptions(t, files, func(cfg *config.Config) {
		cfg.Open.Editor = "echo --goto"
	}, Options{
		CSRFToken: relatedItemTestToken,
		StartEditor: func(string, []string) error {
			started++
			return nil
		},
	})
	external := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(external, []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	candidatePath := filepath.Join(root, "Candidate.md")
	if err := os.Remove(candidatePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, candidatePath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	resp := postRelatedItemRequest(t, ts.URL, relatedItemValues("source", "candidate", "open_editor", ""), relatedItemTestToken)
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusNotFound || started != 0 || !strings.Contains(body, "not found or stale") {
		t.Fatalf("status/started/body = %d/%d/%s", resp.StatusCode, started, body)
	}
}
