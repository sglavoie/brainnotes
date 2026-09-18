package attachments

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// recordVersion is the record format this build writes and reads. A record it
// does not understand is treated as no record at all, for the reason stated on
// readRecord.
const recordVersion = 1

// Record is what one completed upload is, and the whole of what this package
// remembers about it.
//
// It holds a digest and a size rather than any bytes, and the filename it holds
// is the sanitized one — the string a client sent is never stored, because
// nothing would read it and storing it would make this file the one place in the
// vault where an unsanitized name still exists.
//
// RelPath is a vault-relative slash path and is decided at staging time, not at
// commit time. That is what makes two concurrent uploads of `photo.png` land on
// two destinations: the claim is made while both are still private, so the
// commit has nothing left to negotiate.
type Record struct {
	Version   int       `json:"version"`
	ID        string    `json:"id"`
	Owner     string    `json:"owner"`
	Filename  string    `json:"filename"`
	RelPath   string    `json:"rel_path"`
	Size      int64     `json:"size"`
	SHA256    string    `json:"sha256"`
	CreatedAt time.Time `json:"created_at"`
}

// readRecord loads one upload's record.
//
// Absent, undecodable, and a version this build does not understand are all
// [ErrNotFound]. Undecodable is not a corruption to report because the record is
// written whole, fsynced, and only then linked into place — so a file that does
// not decode was never a complete record, and the honest thing to say about the
// upload it would have described is that it is not there. A future version is
// the same answer for a different reason: this build cannot promise to commit
// bytes it cannot fully describe.
func (s *Store) readRecord(id string) (Record, error) {
	data, err := os.ReadFile(s.recordPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return Record{}, ErrNotFound
		}
		return Record{}, fmt.Errorf("attachments: read upload record: %w", err)
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil || record.Version != recordVersion {
		return Record{}, ErrNotFound
	}
	if record.ID != id {
		// The file is named by the id, so a record naming a different one is a
		// record about some other upload and must never be answered with.
		return Record{}, ErrNotFound
	}
	return record, nil
}
