package noteops

import (
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// Capture receipts contain no thought text. They are journaled atomically with
// the append and retained: expiring an additive operation's receipt would make
// an old retry append twice. Undo removes the receipt with its append.
type captureReceipt struct {
	Version      int       `json:"version"`
	ID           string    `json:"id"`
	Digest       string    `json:"digest"`
	RelPath      string    `json:"rel_path"`
	At           time.Time `json:"at"`
	LineNumber   int       `json:"line_number"`
	Created      bool      `json:"created"`
	AddedHeading bool      `json:"added_heading"`
}

func captureReceiptPath(root, id string) (PathResolution, error) {
	if err := validateSourceSaveID(id); err != nil {
		return PathResolution{}, Errorf(CodeInvalidRequest, "capture: invalid request ID")
	}
	return PathForRel(root, ".bnotes/captures/"+vault.HashBytes([]byte(id))+".json")
}

func replayCapture(root string, req CaptureRequest) (*CaptureResult, error) {
	if strings.TrimSpace(req.ID) == "" {
		return nil, nil
	}
	dest, err := captureReceiptPath(root, req.ID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(dest.AbsPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, WrapError(CodeInternal, "capture: read receipt", err)
	}
	var receipt captureReceipt
	if err := json.Unmarshal(data, &receipt); err != nil || receipt.Version != 1 {
		return nil, Errorf(CodeConflict, "capture: receipt cannot be read; check the inbox before starting another capture")
	}
	if receipt.ID != req.ID || receipt.Digest != vault.HashBytes([]byte(req.Text)) {
		return nil, Errorf(CodeConflict, "capture: this request ID belongs to different text; nothing was appended")
	}
	return receipt.result(root, req)
}

func (receipt captureReceipt) result(root string, req CaptureRequest) (*CaptureResult, error) {
	dest, err := resolveCaptureDestination(root, receipt.RelPath)
	if err != nil {
		return nil, err
	}
	req.At = receipt.At
	line, err := captureBullet(req)
	if err != nil {
		return nil, err
	}
	return &CaptureResult{Destination: dest, Line: line, LineNumber: receipt.LineNumber,
		CreatedNote: receipt.Created, AddedHeading: receipt.AddedHeading, Replayed: true}, nil
}

func stageCaptureReceipt(txn *vault.FileTxn, root string, plan capturePlan) error {
	if strings.TrimSpace(plan.request.ID) == "" {
		return nil
	}
	dest, err := captureReceiptPath(root, plan.request.ID)
	if err != nil {
		return err
	}
	receipt := captureReceipt{Version: 1, ID: plan.request.ID,
		Digest: vault.HashBytes([]byte(plan.request.Text)), RelPath: plan.destination.RelPath,
		At: plan.request.At, LineNumber: plan.lineNumber, Created: plan.createdNote, AddedHeading: plan.addedHeading}
	data, err := json.Marshal(receipt)
	if err != nil {
		return WrapError(CodeInternal, "capture: encode receipt", err)
	}
	return txn.Write(dest.AbsPath, append(data, '\n'), 0, vault.ExpectMissing())
}
