package checkpoint

import "time"

// State is what a vault's checkpoint record says, together with the one thing
// the record cannot say: whether the client that wrote it is still there.
type State struct {
	// Announced is whether a record exists at all.
	Announced bool `json:"announced"`
	// Live is whether some process still holds the claim lock. An announced
	// checkpoint that is not live is the leavings of a client that died; writers
	// ignore it and the next Hold overwrites it.
	Live bool `json:"live"`
	// Blocking is the one answer a writer needs: announced and live.
	Blocking bool   `json:"blocking"`
	ID       string `json:"id,omitempty"`
	Phase    Phase  `json:"phase,omitempty"`
	Reason   string `json:"reason,omitempty"`
	PID      int    `json:"pid,omitempty"`
	Host     string `json:"host,omitempty"`
	// AnnouncedAt and Deadline are zero when there is no record.
	AnnouncedAt time.Time `json:"announced_at,omitzero"`
	Deadline    time.Time `json:"deadline,omitzero"`
}

// Status reports whether a checkpoint is holding the vault at root.
//
// It takes no lock on the barrier and creates nothing, so it is safe to call
// from a status command against a vault somebody else is writing. It does
// try-lock the claim lock for an instant when a record exists, which is what
// separates a live checkpoint from a dead one's leavings.
func Status(root string) (State, error) {
	paths, err := resolve(root)
	if err != nil {
		return State{}, err
	}
	rec, err := readRecord(paths.record)
	if err != nil || rec == nil {
		return State{}, err
	}
	live, err := clientLive(paths)
	if err != nil {
		return State{}, err
	}
	return State{
		Announced:   true,
		Live:        live,
		Blocking:    live,
		ID:          rec.ID,
		Phase:       rec.Phase,
		Reason:      rec.Reason,
		PID:         rec.PID,
		Host:        rec.Host,
		AnnouncedAt: rec.AnnouncedAt,
		Deadline:    rec.Deadline,
	}, nil
}
