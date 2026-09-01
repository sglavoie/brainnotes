package vaultgit

import (
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/agent"
	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

// reloadPolicy polls the configuration source and applies what it finds.
//
// Its shape follows internal/web's configManager.Refresh, which solved the same
// problem for the server: fingerprint the bytes, keep the last valid publication
// when the new ones do not decode, and recover on the next valid read without a
// restart. What differs is what "publish" means here — a floor the scheduler
// enforces rather than a document requests read — and that one transition owes
// the vault a snapshot before it may take effect.
func (w *Watcher) reloadPolicy() {
	if w.policy == nil {
		return
	}
	snapshot, err := w.policy()
	if err != nil {
		w.reportInvalidPolicy(snapshot, err)
		return
	}
	next := resolveAutosavePolicy(snapshot.Policy)
	if w.invalidPolicyKey != "" {
		w.invalidPolicyKey = ""
		w.emit(WatchEvent{
			Kind:    WatchEventConfigRecovered,
			Message: "the vault configuration is readable again",
			Policy:  next.Canonical,
		})
	}
	// An unchanged fingerprint is the overwhelmingly common case and it is
	// answered without deciding anything. A source that publishes no fingerprint
	// is handled by adoptPolicy, which is a no-op for a policy already in force.
	if snapshot.Hash != "" && snapshot.Hash == w.policyHash {
		return
	}
	w.policyHash = snapshot.Hash
	w.adoptPolicy(next)
}

// reportInvalidPolicy emits at most one event per distinct broken version.
//
// The dedup key is the byte fingerprint, so editing a broken file into a
// differently broken file is reported again while leaving it alone is not. A
// source that could not read the bytes at all has no fingerprint to offer, and
// its diagnostic stands in for one: it is derived from the same failure, so it
// is stable exactly as long as the failure is.
func (w *Watcher) reportInvalidPolicy(snapshot PolicySnapshot, err error) {
	detail := agent.SanitizedDiagnostic(err)
	key := snapshot.Hash
	if key == "" {
		key = "unreadable\x00" + detail
	}
	if key == w.invalidPolicyKey {
		return
	}
	w.invalidPolicyKey = key
	w.emit(WatchEvent{
		Kind:    WatchEventConfigInvalid,
		Message: "the vault configuration could not be applied, so the last valid autosave policy stays in force",
		Policy:  w.mgr.AutosavePolicy().Canonical,
		Detail:  detail,
	})
}

// adoptPolicy decides what a newly read policy means for the loop.
//
// There are four transitions and they are genuinely different:
//
//   - to the same policy — the file's bytes moved but its meaning did not, so
//     nothing is published and nothing is logged;
//   - enabled to enabled — published immediately, so the scan later in this same
//     poll sees the new floor. A shortened interval can make an already-ready
//     burst due right now and a lengthened one can postpone it, which is the
//     point: the user asked for a floor, not for the next floor;
//   - enabled to off — recorded as intent only. The vault is owed one final
//     snapshot under the outgoing policy first, and until that lands the policy
//     in force is still the old one;
//   - off to enabled — published immediately with the burst reset, so whatever
//     the working tree accumulated while off opens a fresh burst on the next
//     dirty scan rather than being committed instantly on a deadline that
//     expired hours ago.
func (w *Watcher) adoptPolicy(next config.AutosavePolicy) {
	current := w.mgr.AutosavePolicy()

	if next.Disabled {
		if current.Disabled {
			// Already off, so nothing is owed *by this read*. Whether the transition
			// that made it off ever finished is a question about a previous process,
			// and resumeInterruptedOffTransition has already answered it from durable
			// state by the time any poll reaches here.
			return
		}
		// Re-recorded rather than ignored when a transition is already pending, so
		// the intent always names the version most recently read.
		w.pendingOff = true
		w.pendingPolicy = next
		return
	}

	// The target is enabled from here down. A pending transition that never
	// landed is simply abandoned: `off` was never published, so there is nothing
	// to undo and the burst it would have closed is still the live one.
	cancelled := w.pendingOff
	w.pendingOff = false
	w.pendingPolicy = config.AutosavePolicy{}

	switch {
	case current.Disabled:
		w.publishPolicy(next, true, "external autosave is on again; the next scan of a dirty working tree opens a fresh burst")
	case next.Canonical != current.Canonical:
		w.publishPolicy(next, false, "the external autosave floor changed")
	case cancelled:
		w.publishPolicy(next, false, "external autosave stays on; the pending switch to off was withdrawn before it took effect")
	}
}

// publishPolicy makes a policy the one the engine enforces and says so once.
//
// freshBurst asks for the debounce clock to be discarded along with the change,
// which is right for both ends of an off transition and wrong for a change
// between two enabled floors — there, the open burst is exactly what the new
// floor is supposed to reschedule.
func (w *Watcher) publishPolicy(next config.AutosavePolicy, freshBurst bool, message string) {
	if err := w.mgr.SetAutosavePolicy(next); err != nil {
		// The policy is in force either way; only its publication to watch.json
		// failed, and the next state write republishes it.
		w.emit(WatchEvent{
			Kind:    WatchEventError,
			Message: "the new autosave policy could not be published to the vault's watch state",
			Policy:  next.Canonical,
			Detail:  agent.SanitizedDiagnostic(err),
		})
	}
	if freshBurst {
		w.lastFingerprint = ""
		if err := w.mgr.ResetExternalBurst(); err != nil {
			w.emit(WatchEvent{
				Kind:    WatchEventError,
				Message: "the debounce clock could not be reset for the new autosave policy",
				Policy:  next.Canonical,
				Detail:  agent.SanitizedDiagnostic(err),
			})
		}
	}
	w.emit(WatchEvent{Kind: WatchEventConfigChanged, Message: message, Policy: next.Canonical})
}

// resumeInterruptedOffTransition rebuilds a switch to `off` that a previous
// process began and did not finish.
//
// It exists because the intent [adoptPolicy] records is worth exactly one
// process lifetime while the obligation that intent stands for is not. A guard
// starting under an already-`off` configuration cannot tell from that file alone
// whether `off` has been effective for a month or whether it was typed thirty
// seconds before the last process died: the bytes are identical, adoptPolicy
// answers "already off, nothing to do" to both, and in the second case the edits
// the previous process was about to snapshot stay outside the history for as
// long as the vault stays off — which is exactly the state the final snapshot
// exists to prevent.
//
// The vault's own durable record is what separates them. `watch.json` names the
// policy the engine was enforcing when it last wrote that file, `off` is
// published there only by [Watcher.finishOffTransition] — that is, only once the
// owed snapshot has landed — and nothing outside this loop ever publishes a
// policy at all. So a record still naming an enabled floor under an `off`
// configuration is the previous process saying it never got that far.
//
// What this does about it is to put the loop into precisely the state the poll
// that read the edit would have left it in: the outgoing policy in force, the
// transition held as intent. Every guarantee of the live path then applies
// unchanged rather than being restated here — the snapshot bypasses the quiet
// period and the floor, contention retries because the attempt does not consume
// the intent, and a safety failure leaves the vault enabled and blocked instead
// of quietly off.
func (w *Watcher) resumeInterruptedOffTransition() {
	if w.resumeChecked {
		return
	}
	w.resumeChecked = true

	target := w.mgr.AutosavePolicy()
	if !target.Disabled {
		// This process did not start under `off`, so there is no transition to be
		// part-way through. Whatever the record says, the configuration that was
		// loaded asks for snapshots, and adoptPolicy owns every change from here.
		return
	}
	previous, published := previouslyEffectivePolicy(w.mgr.WatchStatus())
	if !published || previous.Disabled {
		// Either no guard has ever published a policy for this vault, or the last
		// one to do so published `off` — which it does only after the owed snapshot
		// landed. Neither is evidence of an interrupted transition, and inventing
		// one would put a commit on the startup of every already-off vault.
		return
	}
	if err := w.mgr.SetAutosavePolicy(previous); err != nil {
		// The policy is restored in memory either way; only its republication to
		// watch.json failed, which is the same trade publishPolicy makes.
		w.emit(WatchEvent{
			Kind:    WatchEventError,
			Message: "the outgoing autosave policy could not be republished while resuming an interrupted switch to off",
			Policy:  previous.Canonical,
			Detail:  agent.SanitizedDiagnostic(err),
		})
	}
	w.pendingOff = true
	w.pendingPolicy = target
}

// previouslyEffectivePolicy reads the policy the vault was last known to be
// enforcing out of its persisted watch state.
//
// An absent token means no guard has ever published one — a vault that has never
// been watched, or one whose state directory was cleared — and that is evidence
// of nothing rather than evidence of an interrupted transition.
//
// A token that is neither `off` nor a spelling this build parses is corruption,
// and it is read as "enabled under a floor this build cannot name". That is the
// fail-closed direction: acting on it costs at most one snapshot of bytes the
// vault was going to commit anyway, while reading it as `off` would silently
// discard an owed one, and discarding it is the failure this whole path exists
// to rule out.
func previouslyEffectivePolicy(state WatchState) (config.AutosavePolicy, bool) {
	token := strings.TrimSpace(state.AutosaveMinInterval)
	if token == "" {
		return config.AutosavePolicy{}, false
	}
	policy, err := config.ParseAutosaveMinInterval(token)
	if err != nil {
		return resolveAutosavePolicy(config.AutosavePolicy{}), true
	}
	return resolveAutosavePolicy(policy), true
}

// finishOffTransition publishes `off` once the vault has been left whole.
//
// It is called from the two places that prove nothing is owed: the scan found an
// empty working tree, and the final snapshot succeeded. Every other path leaves
// the intent standing, which is what makes contention a retry rather than a lost
// transition and a safety failure a refusal rather than a silent switch.
func (w *Watcher) finishOffTransition() {
	if !w.pendingOff {
		return
	}
	target := w.pendingPolicy
	w.pendingOff = false
	w.pendingPolicy = config.AutosavePolicy{}
	w.publishPolicy(target, true, "external autosave is off; the vault is still scanned, protected, and pushed")
}
