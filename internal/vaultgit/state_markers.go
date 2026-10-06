package vaultgit

import (
	"fmt"
	"time"
)

// LoadBlock reports the persisted block marker.
//
// The corrupt case is the interesting one and it fails closed: a blocked.json
// that exists but does not parse still means blocked, and the returned record
// says so. Treating an unreadable marker as absent would make truncating the
// file — which a crash mid-write, a full disk, or a careless `>` all do — the
// way to re-enable mutations, which is precisely backwards. A version this
// build does not understand is handled the same way.
func (s *safetyState) LoadBlock() (record BlockRecord, blocked bool, err error) {
	var stored BlockRecord
	found, decodeErr := s.readJSON(blockedFileName, &stored)
	if !found {
		return BlockRecord{}, false, nil
	}
	if decodeErr != nil {
		return BlockRecord{
			Version:   stateVersion,
			Kind:      KindBlocked,
			Reason:    "the block marker exists but could not be read, so mutation snapshots stay refused",
			Recovery:  corruptMarkerRecovery(s.path(blockedFileName)),
			Operation: "load-block",
		}, true, decodeErr
	}
	if stored.Version != stateVersion {
		return BlockRecord{
			Version:   stored.Version,
			Kind:      KindBlocked,
			Reason:    fmt.Sprintf("the block marker is version %d, which this build does not understand", stored.Version),
			Recovery:  corruptMarkerRecovery(s.path(blockedFileName)),
			Operation: "load-block",
		}, true, nil
	}
	return stored, true, nil
}

func corruptMarkerRecovery(path string) []string {
	return []string{
		"Inspect " + path + " by hand; it is JSON and holds no note contents.",
		"Run `git -C <vault> status` and `git -C <vault> log --oneline -5` to see what the working tree and the history actually hold.",
		"Recover anything missing from the history with `git -C <vault> cat-file blob <oid> > <path>` before clearing the marker.",
		"Delete " + path + " to allow BrainNotes mutations again.",
	}
}

// SaveBlock persists the marker.
//
// Its failure is reported but is not what makes the vault safe. If this write
// fails, the condition that produced it — an ambiguous filesystem change or a ref
// that still will not advance — is unchanged on disk, and
// the next preflight re-derives the same verdict from the same evidence. The
// marker makes the refusal cheap to explain, not real.
func (s *safetyState) SaveBlock(record BlockRecord) error {
	record.Version = stateVersion
	record.ObservedAt = record.ObservedAt.UTC()
	return s.writeJSON(blockedFileName, record)
}

// ClearBlock removes the marker. Legacy deletion migration calls it after
// revalidation; other block kinds require explicit recovery.
func (s *safetyState) ClearBlock() error { return s.remove(blockedFileName) }

// LoadIntent reports the persisted recovery intent.
//
// It fails closed in the same shape as LoadBlock, and for a sharper reason. The
// only way recovery can retire an intent is by proving NewOID is reachable from
// the ref, so an unreadable record is returned with an *empty* NewOID: there is
// nothing to prove reachable, the proof cannot succeed, and the operation stays
// refused until a person looks. Returning "absent" for a file that exists would
// let a truncated record — a crash mid-write, a full disk — silently retire the
// one durable statement that a snapshot may have been lost.
//
// A record whose version this build does not understand is handled identically:
// its NewOID may well be meaningful, but this build cannot claim to know what
// the field means, and guessing is exactly the wrong move here.
func (s *safetyState) LoadIntent() (record IntentRecord, present bool, err error) {
	var stored IntentRecord
	found, decodeErr := s.readJSON(intentFileName, &stored)
	if !found {
		return IntentRecord{}, false, nil
	}
	if decodeErr != nil {
		return IntentRecord{
			Version:    stateVersion,
			Operation:  "load-intent",
			Phase:      IntentPhasePlanned,
			ObservedAt: time.Time{},
			Recovery:   unreadableIntentRecovery(s.path(intentFileName)),
		}, true, decodeErr
	}
	if stored.Version != stateVersion {
		return IntentRecord{
			Version:   stored.Version,
			Operation: "load-intent",
			Phase:     IntentPhasePlanned,
			Recovery:  unreadableIntentRecovery(s.path(intentFileName)),
		}, true, nil
	}
	return stored, true, nil
}

func unreadableIntentRecovery(path string) []string {
	return []string{
		"Inspect " + path + " by hand; it is JSON and holds no note contents.",
		"Run `git -C <vault> status` and `git -C <vault> log --oneline -5` to see whether the interrupted snapshot landed.",
		"Commit anything still uncommitted by hand before continuing.",
		"Delete " + path + " to allow BrainNotes mutation snapshots again.",
	}
}

// SaveIntent persists the intent and, through writeAtomic, fsyncs both the
// record and the directory entry naming it. Unlike SaveBlock, this write's
// failure is decisive: an operation that cannot record its intent must not
// proceed, because the state it is about to create would then be unrecoverable
// by construction.
func (s *safetyState) SaveIntent(record IntentRecord) error {
	record.Version = stateVersion
	record.ObservedAt = record.ObservedAt.UTC()
	if record.LandedAt != nil {
		landedAt := record.LandedAt.UTC()
		record.LandedAt = &landedAt
	}
	if len(record.Paths) > intentPathCap {
		record.Paths = record.Paths[:intentPathCap]
	}
	return s.writeJSON(intentFileName, record)
}

// ClearIntent retires the record. It is called only after the outcome is durable
// or after recovery has proved what happened, never as cleanup for an error
// whose result is unknown.
func (s *safetyState) ClearIntent() error { return s.remove(intentFileName) }
