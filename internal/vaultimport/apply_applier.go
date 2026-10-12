package vaultimport

import (
	"errors"
	"fmt"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// decide is everything between reading the package and writing anything: what
// this disk disagrees with, what a resolution was planned to overrule, and which
// paths are already at the result. It reports whether there is anything left for
// a transaction to do.
//
// It is one step rather than four inline ones because every one of them is a
// refusal that must happen before a destination is touched, and keeping them
// together is what makes that ordering readable at the call site.
func (a *applier) decide() (bool, error) {
	found, err := a.inspect()
	if err != nil {
		return false, err
	}
	if len(found.conflicts) > 0 {
		if err := a.contested(found); err != nil {
			return false, err
		}
	}
	a.mark(found)
	if err := a.concede(); err != nil {
		return false, err
	}
	return found.settled() || a.settled(), nil
}

// decline holds a package whose changes this vault disagrees with, and returns
// the refusal naming every contested path.
//
// The hold is durable and the refusal is not, which is the asymmetry that
// matters: a caller sees the message once, and the next pass — in another
// process, after a reboot — has to be able to see the same thing. A hold that
// could not be written down is joined onto the refusal rather than replacing it,
// because the package is refused either way and the contested content is still
// untouched.
func (a *applier) decline(found *disposition) error {
	refusal := found.refusal(a.manifest.PackageID)
	if err := a.hold(found); err != nil {
		return errors.Join(refusal, err)
	}
	return refusal
}

// contested decides what to do about the conflicts inspect found: an ordinary
// import declines, and a resolution proceeds over exactly the ones it was
// planned against.
//
// The comparison is not a formality. A resolution is applied with the
// producer's expectations replaced by the branches its plan pinned, which is
// what lets the peer's version land over content it did not expect — so a plan
// that no longer describes this vault would be a licence to overwrite whatever
// is there now. Resolve checks the same facts before it starts; this is the
// check made again with the package read and the disk inspected, which is the
// last point before a transaction exists.
func (a *applier) contested(found *disposition) error {
	if a.opts.resolving == nil {
		return a.decline(found)
	}
	return a.opts.resolving.covers(found.conflicts)
}

// concede marks the contested paths a resolution has nothing to do about.
//
// One kind reaches here: the source of a divergent rename, which is contested
// precisely because it already holds what the operation leaves it holding —
// nothing. Staging it would ask the transaction to remove a file that is not
// there. The question is asked of every path rather than of that kind, because
// "the disk is already at the result" is the general statement and the rename is
// only the case that makes it true today.
func (a *applier) concede() error {
	if a.opts.resolving == nil {
		return nil
	}
	for i := range a.changes {
		if a.changes[i].converged {
			continue
		}
		state, err := diskState(a.abs(a.changes[i].change.Path))
		if err != nil {
			return err
		}
		if state == afterState(a.changes[i].change) {
			a.changes[i].converged = true
		}
	}
	return nil
}

// settled reports a resolution whose application has nothing left to stage,
// which disposition.settled cannot answer because it was decided before concede
// ran.
func (a *applier) settled() bool {
	if a.opts.resolving == nil {
		return false
	}
	for _, planned := range a.changes {
		if !planned.converged {
			return false
		}
	}
	return true
}

// mark records which changes need no transaction, so plan stages the rest and
// declare writes down what a recovery must not roll back.
func (a *applier) mark(found *disposition) {
	for i := range a.changes {
		a.changes[i].converged = found.converged[a.changes[i].change.Path]
	}
}

// converge acknowledges and remembers a package this vault had already applied
// the whole of, without applying anything.
//
// It is recorded as applied because that is what it is: every path the package
// names holds exactly what the package would have installed. Its receipt says
// Converged instead of naming a recovery entry, because noteops correctly made
// no entry for an operation that captured nothing. The path observations still
// have to cover the package exactly before that receipt can be published.
func (a *applier) converge() (*Result, error) {
	result, acknowledged, ackErr := a.acknowledgeConvergence()
	if !acknowledged {
		return nil, ackErr
	}
	if err := a.rememberConvergence(result.Receipt); err != nil {
		return result, errors.Join(ackErr, err)
	}
	result.Cleanup = a.cleanup
	return result, ackErr
}

func (a *applier) rememberConvergence(receipt *vaultsync.Receipt) error {
	next := a.state.clone()
	next.Applied[a.manifest.PackageID] = Record{
		OperationID:  a.operation.OperationID,
		OriginDevice: a.manifest.OriginDevice,
		Sequence:     a.manifest.Sequence,
		Converged:    true,
		Receipt:      receipt,
	}
	released := a.release(next)
	if err := a.saveState(next); err != nil {
		return err
	}
	a.state = next
	a.discard(released)
	return nil
}

// applier is one package's journey through this vault, from accepted manifest to
// published receipt.
type applier struct {
	root      string
	opts      Options
	local     *vaultsync.Enrollment
	state     *State
	manifest  *vaultsync.Manifest
	operation *vaultsync.Operation
	changes   []plannedChange
	// cleanup is a scratch file the transaction could not unlink after the
	// destination it belonged to was durably written. See Result.Cleanup.
	cleanup error
}

// plannedChange is one change with its payload bytes already read and verified,
// or no bytes at all when it is a tombstone.
type plannedChange struct {
	change vaultsync.Change
	data   []byte
	// converged means this path already holds what the change installs, so the
	// transaction skips it. It is still read back and acknowledged: the receipt
	// states what the vault holds, and it holds this. See applier.mark.
	converged bool
}

// accept answers the two questions that must be settled before this vault is
// touched: may this package be applied here at all, and is it one this applier
// can apply as a single local operation.
func accept(root, packageDir string, opts Options) (*applier, error) {
	local, err := vaultsync.Load(root)
	if err != nil {
		return nil, err
	}
	manifest, err := vaultsync.Accept(local, packageDir)
	if err != nil {
		return nil, err
	}
	operation, err := singleOperation(manifest)
	if err != nil {
		return nil, err
	}
	state, err := loadState(root, local)
	if err != nil {
		return nil, err
	}
	return &applier{
		root:      root,
		opts:      opts,
		local:     local,
		state:     state,
		manifest:  manifest,
		operation: operation,
	}, nil
}

// singleOperation returns the one operation the package carries.
//
// An empty package cannot reach here — Manifest.Validate refuses one — so the
// zero case is a second statement of that refusal rather than a live branch, and
// it is stated because the alternative is a nil dereference if that ever changes.
func singleOperation(m *vaultsync.Manifest) (*vaultsync.Operation, error) {
	switch len(m.Operations) {
	case 1:
		return &m.Operations[0], nil
	case 0:
		return nil, fmt.Errorf("%w: %s carries no operation", vaultsync.ErrMalformedPackage, m.PackageID)
	default:
		return nil, fmt.Errorf("%w: %s carries %d", ErrMultipleOperations, m.PackageID, len(m.Operations))
	}
}

// read loads every payload the operation needs, before the vault is touched.
//
// All of them, and up front. A package is applied as one transaction, so bytes
// that turn out to be unreadable halfway through would be discovered with some
// destinations already staged; reading here makes an incomplete delivery an
// ErrIncompletePackage that changed nothing. vaultsync.ReadPayload re-verifies
// each digest as it reads rather than trusting the VerifyPayloads that Accept
// just ran, because the transport is still writing into this directory.
func (a *applier) read(packageDir string) error {
	changes := make([]plannedChange, 0, len(a.operation.Changes))
	for _, change := range a.operation.Changes {
		planned := plannedChange{change: change}
		if change.Action == vaultsync.ActionWrite {
			data, err := vaultsync.ReadPayload(packageDir, a.manifest, change.Payload)
			if err != nil {
				return err
			}
			planned.data = data
		}
		changes = append(changes, planned)
	}
	a.changes = changes
	return nil
}
