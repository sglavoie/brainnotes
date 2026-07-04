package cockpit

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
)

const taskIDPrefix = "task_"

// taskSignature derives a stable identity for a task from its rule kind, the
// grouping detail, and the affected notes (relative path, location, and file
// hash). Values are length-prefixed before hashing so that concatenating two
// fields can never collide with a different split of the same bytes.
func taskSignature(kind RuleKind, detail string, affected []AffectedNote) string {
	h := sha256.New()
	writeSignatureValue(h, "version", "cockpit-task-v1")
	writeSignatureValue(h, "kind", string(kind))
	writeSignatureValue(h, "detail", detail)
	for _, note := range affected {
		writeSignatureValue(h, "rel", note.RelPath)
		writeSignatureValue(h, "location", note.Location)
		writeSignatureValue(h, "hash", note.FileHash)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func writeSignatureValue(h hash.Hash, label, value string) {
	_, _ = fmt.Fprintf(h, "%s:%d:%s\n", label, len(value), value)
}
