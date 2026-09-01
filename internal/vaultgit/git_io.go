package vaultgit

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"strings"
)

// boundedBuffer keeps at most limit bytes and records that it dropped the rest,
// so a git command that fails noisily cannot consume memory proportional to its
// own complaint.
type boundedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	written := len(p)
	if remaining := b.limit - b.buf.Len(); remaining > 0 {
		if remaining > len(p) {
			remaining = len(p)
		}
		_, _ = b.buf.Write(p[:remaining])
		if remaining < len(p) {
			b.truncated = true
		}
	} else if len(p) > 0 {
		b.truncated = true
	}
	return written, nil
}

func (b *boundedBuffer) String() string {
	if b.truncated {
		return b.buf.String() + " …"
	}
	return b.buf.String()
}

// countingHasher hashes a stream and counts it, so a blob's sha256 and size are
// obtained in one pass without the blob ever being held in memory. A vault
// holds attachments; some of them are tens of megabytes.
type countingHasher struct {
	h    hash.Hash
	size int64
}

func newCountingHasher() *countingHasher { return &countingHasher{h: sha256.New()} }

func (c *countingHasher) Write(p []byte) (int, error) {
	c.size += int64(len(p))
	return c.h.Write(p)
}

func (c *countingHasher) sum() string { return hex.EncodeToString(c.h.Sum(nil)) }

// nulFields splits NUL-delimited output. A trailing empty field from the final
// delimiter is dropped; interior empty fields are impossible because git never
// emits an empty path.
func nulFields(out []byte) []string {
	fields := strings.Split(string(out), "\x00")
	for len(fields) > 0 && fields[len(fields)-1] == "" {
		fields = fields[:len(fields)-1]
	}
	return fields
}

// nulList encodes paths for a command that reads them from stdin. This is the
// only safe way to hand a note name to git: `--stdin -z` makes a leading dash,
// a newline, a comma, and a non-UTF-8 byte sequence all data. The comma matters
// specifically — `update-index --cacheinfo <mode>,<oid>,<path>` splits on
// commas and silently records the wrong path for a note whose name has one.
func nulList(paths []string) []byte {
	var buf bytes.Buffer
	for _, p := range paths {
		buf.WriteString(p)
		buf.WriteByte(0)
	}
	return buf.Bytes()
}
