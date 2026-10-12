package vaultimport

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"slices"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// finishResolutions carries every resolution this vault was interrupted inside
// to its end, and is the first thing every entry point does.
//
// One question decides each of them, and it is not "how far did it get" but
// "did the operation it resolves land". A package that is not in the applied
// ledger was rolled back or never started, so the resolution is abandoned and
// the package stays held — a person re-previews against whatever the vault
// holds now, which is the only honest thing to offer when the reading their
// choice was made against is gone. A package that is in the ledger is one this
// vault currently agrees with the peer about, against somebody's decision, so
// the revision is made: that is the state the whole arrangement exists to get
// out of, and leaving it would silently resolve the conflict the other way.
func finishResolutions(root string, opts Options) (revised, abandoned []string, why map[string]error, err error) {
	local, err := vaultsync.Load(root)
	if err != nil {
		return nil, nil, nil, err
	}
	state, err := loadState(root, local)
	if err != nil {
		return nil, nil, nil, err
	}
	why = map[string]error{}
	for _, packageID := range slices.Sorted(maps.Keys(state.Resolutions)) {
		done, reason, err := finishResolution(root, state, state.Resolutions[packageID], opts)
		// Recorded before the error is acted on, and that order is the point. A
		// reason means the promise is already out of the state file, so an error
		// raised by the same call — staged bytes that would not delete — has no
		// later pass to hand the news to: returning early without recording it
		// would be the one way somebody's decision disappears silently.
		if reason != nil {
			abandoned = append(abandoned, packageID)
			why[packageID] = reason
		}
		if err != nil {
			return revised, abandoned, why, err
		}
		if done {
			revised = append(revised, packageID)
		}
	}
	// A scratch directory that could not be collected is not a reason to refuse
	// every import in the vault, and a recovery pass has no result to carry the
	// failure on — the same judgement finishResolution makes about its own
	// housekeeping. It is attempted again on the next pass.
	_ = collectStaged(root)
	return revised, abandoned, why, nil
}

// collectStaged removes every staged-revision directory no promise names.
//
// It is the other half of declareResolution's ordering, and the counterpart of
// collectRetained. Content is staged before the record that names it, so a
// crash in between leaves bytes nothing points at; and abandonResolution clears
// the record before removing them, so a crash in *that* gap leaves the same
// thing. Neither is dangerous and neither is retried by the process that would
// have retried it, since that process is the one that died. This is the sweep
// that eventually takes them away, and it runs from finishResolutions so it
// reaches a vault whose channel has never been drained.
//
// A directory whose name is not a package name is left alone rather than swept,
// which means "eventually" excludes it. That is deliberate and is the same rule
// collectRetained follows: the name comes off the disk, and joining an unchecked
// one onto a path to remove it recursively is a worse thing to be wrong about
// than a directory nobody collects. Nothing this package writes can produce such
// a name, so one being there is already a statement that something else put it
// there.
func collectStaged(root string) error {
	local, err := vaultsync.Load(root)
	if err != nil {
		return err
	}
	state, err := loadState(root, local)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(resolutionsRoot(root))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("vaultimport: list staged revisions %q: %w", resolutionsRoot(root), err)
	}
	var failures []error
	for _, entry := range entries {
		if _, promised := state.Resolutions[entry.Name()]; promised || !vaultsync.PackageName(entry.Name()) {
			continue
		}
		failures = append(failures, discardStaged(root, entry.Name()))
	}
	return errors.Join(failures...)
}

// finishResolution decides one of them, and reports whether a revision was made
// and whether the promise was dropped.
//
// A housekeeping failure is dropped rather than reported, which is the one
// place in this package that does so and is the same judgement Result.Cleanup
// encodes with nowhere to put it: a recovery pass has no result to carry it on,
// the vault is correct either way, and refusing over a scratch directory would
// block every import in a vault whose resolution is complete.
//
// The second reason to drop a promise is a revision the vault has moved past.
// Every step is pinned to what applying the operation left on the path, so a
// note edited between the application and the revision refuses the whole
// transaction with the edit intact — and that refusal is permanent, because
// nothing puts the note back. Keeping the promise would mean re-trying it from
// every entry point forever, and since this runs before any of them do, one
// person's typing would refuse every import, drain, resolution and recovery
// this vault ever attempts again. So the decision is abandoned: the edit is
// newer than the choice, the rejected version is still in the recovery and
// content histories the application wrote, and nothing is lost that a person
// cannot see. It is reported on Recovery.Abandoned rather than silently, since
// a decision that did not take effect is exactly the thing a person needs told.
func finishResolution(root string, state *State, record *Plan, opts Options) (done bool, why error, err error) {
	if record == nil {
		return false, nil, fmt.Errorf("%w: a promised resolution names no plan", ErrCorruptState)
	}
	if _, applied := state.Applied[record.PackageID]; !applied {
		cleared, clearErr := abandonResolution(root, record, opts)
		return false, abandonmentOf(cleared, errApplicationNeverLanded), clearErr
	}
	err = reviseResolution(root, record, opts, &Resolution{PackageID: record.PackageID})
	if err != nil && unkeepable(err) {
		// The reason is carried out rather than dropped with the error. "Your edit
		// was newer" and "this vault's staged copy of your merge is damaged" are
		// both abandonments and are not remotely the same news, and a package name
		// on a list cannot tell them apart.
		cleared, clearErr := abandonResolution(root, record, opts)
		return false, abandonmentOf(cleared, err), clearErr
	}
	return err == nil, nil, err
}

// abandonmentOf is the reason to report for a promise abandonResolution says it
// cleared, and nothing at all for one it says it did not.
//
// It keeps the rule that matters in one place: the news is announced only once
// it is true. A clearResolution that failed leaves the decision in the state
// file for the next pass to make again, so calling it dropped would be an
// announcement about something that has not happened — and the next pass would
// announce it a second time when it does.
func abandonmentOf(cleared bool, why error) error {
	if !cleared {
		return nil
	}
	return why
}

// errApplicationNeverLanded is the reason for the other abandonment, which is
// not a failure and so has no error of its own to report.
var errApplicationNeverLanded = errors.New(
	"vaultimport: the application the decision was made about never landed, so the package is still held and can be decided again")

// unkeepable reports a promise no later pass can keep, whatever it runs on.
//
// Three sentinels say that, and they are the whole list. The revision was
// refused by what its paths hold now (ErrConflict, the case above). The content
// it would install is gone (ErrResolutionUnavailable, from a staged file that
// no longer exists). Or what is still there is not what it is named
// (ErrCorruptState, from a staged digest that disagrees with its own file) —
// and writing those bytes anyway is exactly the substitution the digest is
// checked to prevent.
//
// What makes abandoning safe is that none of the three can reach here with a
// half-made change behind it, and for two of them that is because they are
// raised before the revision writes anything at all. ErrConflict is the one
// that needs an argument. It can be raised *after* the transaction captured the
// journal — a configuration pin re-checked around an individual change, the
// case errors.go describes — so a refusal can leave a manifest-less entry
// directory and compensated-back content. write takes that back out before
// returning, and returns the refusal only when the unwind succeeded; an unwind
// that failed is reported as its own error, never joined with this sentinel,
// precisely so it cannot be answered with an abandonment. So ErrConflict
// arriving here is already a statement that nothing is half-made.
//
// A failure raised *during* the unwind is ErrStrandedImport and is deliberately
// absent from this list — see putBack.
//
// One arrival is outside that argument and is self-limiting rather than
// reasoned about: clearResolution runs after the revision has committed and
// finalized, and its own state load can fail with ErrCorruptState. Abandoning
// goes through the same load, so it fails identically, the promise is kept, and
// the error is returned rather than acted on.
//
// The distinction each one has to earn is transient versus permanent, not
// serious versus minor, because this runs ahead of every import, drain,
// resolution and recovery: a promise kept over a failure that will not clear
// stops the vault synchronizing at all. Anything not on this list — an I/O
// error, a lock somebody holds, the unwind's own racing refusal — is returned
// as itself and tried again on the next pass.
func unkeepable(err error) bool {
	return errors.Is(err, ErrConflict) ||
		errors.Is(err, ErrResolutionUnavailable) ||
		errors.Is(err, ErrCorruptState)
}

// IsUnrecordableDecision answers whether a revision failure is one no later pass
// will retry, and it is unkeepable's door for a caller that has to say what
// happens next rather than decide it.
//
// The two answers are not degrees of the same news. An ordinary revision failure
// leaves the promise in the state file and the next pass makes the second commit,
// so a vault holding the peer's version holds it for a moment. One of these three
// abandons the promise instead, so that vault holds the version somebody decided
// against until they decide again — and the difference is the whole of what a
// person needs told.
func IsUnrecordableDecision(err error) bool { return unkeepable(err) }

// abandonUnlessLanded drops a promise the vault has no use for, and keeps one
// whose application is still being decided.
//
// The two states it keeps the promise for are the two an application can be in
// without having returned a Result: already recorded as applied — which the
// application's own in-process recovery can do on its way out — and still
// pending, where the next pass decides the direction and finishResolution then
// asks this same question of an answer instead of a guess.
func abandonUnlessLanded(root string, record *Plan, opts Options) error {
	local, err := vaultsync.Load(root)
	if err != nil {
		return err
	}
	state, err := loadState(root, local)
	if err != nil {
		return err
	}
	if _, applied := state.Applied[record.PackageID]; applied {
		return nil
	}
	if state.Pending != nil && state.Pending.Manifest != nil &&
		state.Pending.Manifest.PackageID == record.PackageID {
		return nil
	}
	// The caller here is resolveLocked, which is about to return the refusal that
	// stopped the application; whether the promise was cleared changes nothing it
	// can say, so only the failure is passed on.
	//
	// It is usually the first to ask, and sometimes the second. An application
	// refused before it declared its attempt — a stale plan, a precondition that
	// failed at staging — never reaches applier.unwind, and this is the only thing
	// that drops the promise. One refused *after* does reach it, and unwind is a
	// whole recovery pass that sees this same unapplied promise first; by the time
	// control gets back here the state no longer names the package and
	// clearResolution is a no-op. Both orders are correct and neither caller may
	// assume the other ran, which is why this asks rather than checks.
	_, err = abandonResolution(root, record, opts)
	return err
}

// abandonResolution forgets a resolution whose application never landed, and
// says whether the promise is actually gone.
//
// The two answers are reported separately because they have separate
// consequences. clearResolution runs first and is what makes the abandonment
// durable: a failure there leaves the promise in the state file, the next pass
// decides it again, and a caller must not tell anybody their choice was
// dropped. discardStaged runs second and only removes bytes nothing names any
// more; its failure is worth returning, but by then the decision is gone for
// good, and treating that as "still promised" would mean nobody is ever told.
//
// A plan that revises nothing is never written to the state file at all — see
// declareResolution — so there is nothing to clear and the promise is vacuously
// gone.
func abandonResolution(root string, record *Plan, opts Options) (cleared bool, err error) {
	if !record.Revises() {
		return true, nil
	}
	if err := clearResolution(root, record.PackageID, opts); err != nil {
		return false, err
	}
	return true, discardStaged(root, record.PackageID)
}

// clearResolution stops the state naming a package's revision. It runs before
// the staged content is removed, the ordering every store in this package is
// written under: a record naming content that is gone is a promise nothing can
// keep, where content nothing names is removable at leisure.
func clearResolution(root, packageID string, opts Options) error {
	local, err := vaultsync.Load(root)
	if err != nil {
		return err
	}
	state, err := loadState(root, local)
	if err != nil {
		return err
	}
	if _, promised := state.Resolutions[packageID]; !promised {
		return nil
	}
	next := state.clone()
	delete(next.Resolutions, packageID)
	if err := saveStateWith(root, next, opts.writeState); err != nil {
		return err
	}
	return nil
}
