package noteops

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCaptureReceiptReplaysAfterOtherCaptures(t *testing.T) {
	root := t.TempDir()
	req := captureRequest("first thought", 30)
	req.ID = "attempt-1"
	first, err := ApplyCapture(root, "Inbox.md", req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyCapture(root, "Inbox.md", captureRequest("another thought", 31), nil); err != nil {
		t.Fatal(err)
	}
	req.At = req.At.Add(time.Hour)
	replay, err := ApplyCapture(root, "ChangedInbox.md", req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Replayed || replay.Line != first.Line || replay.Destination.RelPath != "Inbox.md" {
		t.Fatalf("incorrect replay: %+v", replay)
	}
	data, err := os.ReadFile(filepath.Join(root, "Inbox.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "first thought") != 1 {
		t.Fatalf("duplicated capture: %s", data)
	}
	assertCaptureReceiptIdentity(t, root, req)
}

func TestCaptureReceiptConcurrentRetriesAppendOnce(t *testing.T) {
	root := t.TempDir()
	req := captureRequest("one submission", 30)
	req.ID = "concurrent-attempt"
	var wg sync.WaitGroup
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := ApplyCapture(root, "Inbox.md", req, nil); err != nil {
				t.Errorf("capture: %v", err)
			}
		}()
	}
	wg.Wait()
	data, err := os.ReadFile(filepath.Join(root, "Inbox.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "one submission") != 1 {
		t.Fatalf("duplicated: %s", data)
	}
}

func TestCaptureReceiptCorruptionRefusesRetry(t *testing.T) {
	root := t.TempDir()
	req := captureRequest("one submission", 30)
	req.ID = "corrupt-attempt"
	if _, err := ApplyCapture(root, "Inbox.md", req, nil); err != nil {
		t.Fatal(err)
	}
	receipt, err := captureReceiptPath(root, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(receipt.AbsPath, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyCapture(root, "Inbox.md", req, nil); err == nil {
		t.Fatal("corrupt receipt permitted another append")
	}
}

func assertCaptureReceiptIdentity(t *testing.T, root string, req CaptureRequest) {
	t.Helper()
	req.Text = "different thought"
	if _, err := ApplyCapture(root, "Inbox.md", req, nil); err == nil {
		t.Fatal("reused ID accepted different text")
	}
	receipt, err := captureReceiptPath(root, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(receipt.AbsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "thought") {
		t.Fatal("receipt contains thought text")
	}
}

func TestCaptureUndoRemovesReceiptWithAppend(t *testing.T) {
	root := t.TempDir()
	req := captureRequest("undoable capture", 30)
	req.ID = "undo-attempt"
	first, err := ApplyCapture(root, "Inbox.md", req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Undo(root, first.Journal.ID, UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	receipt, err := captureReceiptPath(root, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(receipt.AbsPath); !os.IsNotExist(err) {
		t.Fatalf("receipt survives undo: %v", err)
	}
	retry, err := ApplyCapture(root, "Inbox.md", req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if retry.Replayed {
		t.Fatal("undone capture was reported as still applied")
	}
}
