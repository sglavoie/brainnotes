package vaultexport

import (
	"context"
	"fmt"

	"github.com/sglavoie/sglavoie_brainnotes/internal/checkpoint"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// Reset clears the cursor, the path ledger, and any reservation, keeping the
// identity and the sequence.
//
// It is the remedy for the two halts that are not faults — a rewritten history
// and an advanced epoch — and it re-baselines rather than resuming: the next
// export describes the whole portable tree as if the peer held nothing. The
// sequence is *kept* rather than restarted, because a peer that saw the old
// stream must see the new one as later, and reusing a number the peer already
// has would make two different packages the same position in this device's
// history.
//
// A reservation is dropped but its number is *spent*, not released. Reset cannot
// tell which of resolveReservation's three outcomes that publication had — that
// answer is in the channel, and Reset does not look there — so it takes the only
// safe one. Releasing the number would hand it to the next pass while a complete
// package carrying it may already be on its way to the peer, which is precisely
// the collision the sequence is kept for.
//
// The import ledger is kept too. An entry is written before the commit that
// applies it exists and is consumed only when a replay reaches that commit, so
// Reset can land in between — and the commits after the new baseline's pin may
// themselves be applied imports. Dropping the ledger would make the next replay
// read one of those commits as a local change and send the peer its own operation
// straight back.
func Reset(root string) error {
	local, err := vaultsync.Load(root)
	if err != nil {
		return err
	}
	barrier, err := joinBarrier(root)
	if err != nil {
		return err
	}
	defer barrier()
	release, err := vault.LockPaths(root, StatePath(root))
	if err != nil {
		return fmt.Errorf("vaultexport: lock export state: %w", err)
	}
	defer release()

	current, err := loadState(root, local)
	if err != nil {
		return err
	}
	next := current.clone()
	next.Epoch = local.Epoch
	next.Branch = ""
	next.Commit = ""
	next.Baseline = ""
	if current.Pending != nil && current.Pending.Sequence > next.Sequence {
		next.Sequence = current.Pending.Sequence
	}
	next.Pending = nil
	next.Paths = map[string]string{}
	return saveState(root, next)
}

// RebaselineAfterReviewContext writes a fresh producer ledger for a reviewed
// enrollment. sequence is retained only when this device retained its identity;
// a restored device rotates identity and safely starts the new stream at zero.
// The old ledger and channels must have been archived by the review coordinator
// before this is called.
func RebaselineAfterReviewContext(ctx context.Context, root string, sequence uint64) error {
	local, err := vaultsync.Load(root)
	if err != nil {
		return err
	}
	_, barrier, err := joinBarrierContext(ctx, root)
	if err != nil {
		return err
	}
	defer barrier()
	release, err := vault.LockPaths(root, StatePath(root))
	if err != nil {
		return fmt.Errorf("vaultexport: lock export state: %w", err)
	}
	defer release()
	next := newState(local)
	next.Sequence = sequence
	return saveState(root, next)
}

// hexObjectName reports whether s is a full lowercase hex Git object name.
// Restated here rather than reached for across the package boundary: this is a
// check on bytes read back off a disk, and it has to hold whether or not
// internal/vaultgit is ever consulted about them.
func hexObjectName(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// joinBarrier enlists a synchronization-state write in the vault's checkpoint
// barrier, which every such write takes before it takes the state lock. The
// order is the one internal/checkpoint documents and is not negotiable: a writer
// that joined after a vault lock would close a cycle against a checkpoint's
// settle pass.
func joinBarrier(root string) (func(), error) {
	_, release, err := joinBarrierContext(context.Background(), root)
	return release, err
}

func joinBarrierContext(ctx context.Context, root string) (context.Context, func(), error) {
	joinedCtx, release, err := checkpoint.JoinContext(ctx, root)
	if err != nil {
		return ctx, nil, fmt.Errorf("vaultexport: %w", err)
	}
	return joinedCtx, release, nil
}
