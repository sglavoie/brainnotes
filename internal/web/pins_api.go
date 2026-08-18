package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/pins"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

const (
	notePinSetAPIRoutePath     = "/api/pins/set"
	notePinRemoveAPIRoutePath  = "/api/pins/remove"
	notePinReorderAPIRoutePath = "/api/pins/reorder"
)

// pinnedNotesRequest is the prologue every pin endpoint shares with
// handleNoteFrontmatterSet: the parsed form, the CSRF check, the writability
// gate, the pinned configuration, and the note the request names.
//
// The snapshot is refreshed rather than read from cache because the ranks are
// computed here, on the server, from the pinned list as it is on disk right
// now; planning against a snapshot up to one TTL old would interpolate between
// ranks that no longer exist.
type pinnedNotesRequest struct {
	cfg      *config.Config
	pin      configPin
	snapshot vaultSnapshot
	source   noteops.PathResolution
}

func (s *Server) beginPinnedNotesRequest(w http.ResponseWriter, r *http.Request, route string) (pinnedNotesRequest, bool) {
	if r.URL.Path != route {
		http.NotFound(w, r)
		return pinnedNotesRequest{}, false
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeManagementErrorStatus(w, http.StatusMethodNotAllowed, noteops.Errorf(noteops.CodeInvalidRequest, "method not allowed"))
		return pinnedNotesRequest{}, false
	}
	if err := r.ParseForm(); err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInvalidRequest, "invalid request form", err))
		return pinnedNotesRequest{}, false
	}
	if !csrfTokenMatches(s.csrfToken, requestCSRFToken(r)) {
		writeManagementError(w, noteops.Errorf(noteops.CodeForbidden, "csrf token required"))
		return pinnedNotesRequest{}, false
	}
	if err := s.ensureConfigWritable(); err != nil {
		writeManagementError(w, err)
		return pinnedNotesRequest{}, false
	}
	cfg, _, pin := s.pinnedConfig()

	snapshot, err := s.state.refreshSnapshot()
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "pins: snapshot failed", err))
		return pinnedNotesRequest{}, false
	}
	source, err := s.resolveManagementSource(r)
	if err != nil {
		writeManagementError(w, err)
		return pinnedNotesRequest{}, false
	}
	return pinnedNotesRequest{cfg: cfg, pin: pin, snapshot: snapshot, source: source}, true
}

// handleNotePinSet appends the named note to the end of the pinned list. A note
// that is already pinned keeps its rank, matching `bn pin`.
func (s *Server) handleNotePinSet(w http.ResponseWriter, r *http.Request) {
	req, ok := s.beginPinnedNotesRequest(w, r, notePinSetAPIRoutePath)
	if !ok {
		return
	}

	kind, err := pinnedNoteKind(req.source.AbsPath)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	if kind != pins.NotPinned {
		writePinnedNotesResult(w, "Note already pinned.", req.source.RelPath, "")
		return
	}

	plan, err := pins.PlanAppend(pins.List(s.vault.Root, req.snapshot.notes), req.source.RelPath)
	if err != nil {
		writeManagementError(w, pinnedNotesPlanError(err))
		return
	}
	s.applyPinnedNotesPlan(w, req, plan, "bn web pins set", "Pinned note.", "Pins unchanged.")
}

// handleNotePinRemove drops the `pinned` key entirely rather than writing
// `pinned: false`, so `--has pinned` keeps agreeing with `--pinned`.
func (s *Server) handleNotePinRemove(w http.ResponseWriter, r *http.Request) {
	req, ok := s.beginPinnedNotesRequest(w, r, notePinRemoveAPIRoutePath)
	if !ok {
		return
	}

	result, err := noteops.UnsetFrontmatter(s.vault.Root, req.source.RelPath, []string{pins.PropertyName}, &req.cfg.Frontmatter,
		append(frontmatterConfigPinOptions(req.pin), noteops.WithFrontmatterCommand("bn web pins remove"))...)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	if result.Changed {
		s.state.invalidate()
	}
	message := "Unpinned note."
	if !result.Changed {
		message = "Note was not pinned."
	}
	writePinnedNotesResult(w, message, req.source.RelPath, managementJournalID(result.Journal))
}

// handleNotePinReorder moves an already-pinned note into the gap named by its
// two neighbours. The request carries note keys only, never a rank: the new
// rank is the midpoint of the neighbouring ranks as they are on disk, so a
// stale browser cannot write an order it computed against a list that moved.
// A pair that no longer straddles one gap is refused as a conflict rather than
// interpolated into whatever gap those two ranks now bracket.
//
// The list the gap was validated against is carried into the commit as a
// precondition, so a gap that another writer filled between validation and
// commit is refused instead of interpolated into twice.
func (s *Server) handleNotePinReorder(w http.ResponseWriter, r *http.Request) {
	req, ok := s.beginPinnedNotesRequest(w, r, notePinReorderAPIRoutePath)
	if !ok {
		return
	}

	before, err := s.resolvePinnedNeighbor(req.snapshot, r.Form.Get("before"))
	if err != nil {
		writeManagementError(w, err)
		return
	}
	after, err := s.resolvePinnedNeighbor(req.snapshot, r.Form.Get("after"))
	if err != nil {
		writeManagementError(w, err)
		return
	}

	entries := pins.List(s.vault.Root, req.snapshot.notes)
	plan, err := pins.PlanMove(entries, req.source.RelPath, before, after)
	if err != nil {
		writeManagementError(w, pinnedNotesPlanError(err))
		return
	}
	s.applyPinnedNotesPlan(w, req, plan, "bn web pins reorder", "Reordered pins.", "Pin order unchanged.",
		pinnedListRequirements(s.vault.Root, req.snapshot, entries, plan.Assignments)...)
}

// pinnedListRequirements turns the pinned list a move was validated against
// into commit preconditions: the revision of every pin the plan does not
// already write.
//
// Adjacency is a property of the whole list rather than of the two named
// neighbours, so the neighbours' own revisions cannot express it. Two requests
// moving different notes into one gap change neither neighbour, and both would
// otherwise commit the same interpolated rank even though only the first one
// planned against the list it landed in. Requiring every other pin's revision
// makes the second commit fail while the transaction stages, before any note is
// replaced and before the journal captures anything.
func pinnedListRequirements(root string, snapshot vaultSnapshot, entries []pins.Entry, assignments []pins.Assignment) []noteops.FrontmatterOption {
	written := make(map[string]bool, len(assignments))
	for _, assignment := range assignments {
		written[assignment.RelPath] = true
	}
	loaded := make(map[string]*vault.Note, len(snapshot.notes))
	for _, note := range snapshot.notes {
		loaded[noteVaultPath(root, note)] = note
	}

	options := make([]noteops.FrontmatterOption, 0, len(entries))
	for _, entry := range entries {
		if written[entry.RelPath] {
			continue
		}
		note := loaded[entry.RelPath]
		hash := note.SourceHash()
		if hash == "" {
			continue
		}
		options = append(options, noteops.WithFrontmatterRequire(note.Path, vault.ExpectHash(hash)))
	}
	return options
}

// resolvePinnedNeighbor turns a neighbour note key into a vault-relative path.
// An empty key is an end of the list, not a missing note.
func (s *Server) resolvePinnedNeighbor(snapshot vaultSnapshot, key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", nil
	}
	_, _, source, err := noteops.ResolveExistingRouteNote(s.vault.Root, snapshot.notes, snapshot.graph, key)
	if err != nil {
		return "", err
	}
	return source.RelPath, nil
}

// applyPinnedNotesPlan commits a plan and answers on the shared management
// contract. A plan with no assignments writes nothing, which is what a drag
// that lands where the note already was must do. requires carries the
// preconditions the plan was validated under, so a plan whose inputs moved
// between validation and commit is refused rather than applied.
func (s *Server) applyPinnedNotesPlan(w http.ResponseWriter, req pinnedNotesRequest, plan pins.Plan, command, changedMessage, unchangedMessage string, requires ...noteops.FrontmatterOption) {
	if len(plan.Assignments) == 0 {
		writePinnedNotesResult(w, unchangedMessage, req.source.RelPath, "")
		return
	}
	writes, err := pinnedNotesWrites(plan.Assignments, &req.cfg.Frontmatter)
	if err != nil {
		writeManagementError(w, err)
		return
	}

	opts := append(frontmatterConfigPinOptions(req.pin), noteops.WithFrontmatterCommand(command))
	opts = append(opts, requires...)
	if s.beforePinnedNotesCommit != nil {
		s.beforePinnedNotesCommit()
	}
	var result *noteops.FrontmatterResult
	if plan.Renormalized || len(writes) > 1 {
		// A renormalize rewrites the whole list, so it goes through one
		// transaction: half a renumbering is a scrambled order, not a partial one.
		result, err = noteops.SetFrontmatterAtomic(s.vault.Root, writes, &req.cfg.Frontmatter, opts...)
	} else {
		result, err = noteops.SetFrontmatter(s.vault.Root, writes[0].RelPath, writes[0].Mutations, &req.cfg.Frontmatter, opts...)
	}
	if err != nil {
		writeManagementError(w, err)
		return
	}
	if result.Changed {
		s.state.invalidate()
	}
	message := changedMessage
	if !result.Changed {
		message = unchangedMessage
	}
	writePinnedNotesResult(w, message, req.source.RelPath, managementJournalID(result.Journal))
}

// pinnedNotesWrites types every rank as a float explicitly, so a pin lands in a
// vault whose brainnotes.toml declares no `pinned` property.
func pinnedNotesWrites(assignments []pins.Assignment, fm *config.FrontmatterConfig) ([]noteops.NoteFrontmatterWrite, error) {
	writes := make([]noteops.NoteFrontmatterWrite, 0, len(assignments))
	for _, assignment := range assignments {
		mutation, err := noteops.NewFrontmatterMutation(pins.PropertyName, "=",
			strconv.FormatFloat(assignment.Rank, 'f', -1, 64), "float", fm)
		if err != nil {
			return nil, err
		}
		writes = append(writes, noteops.NoteFrontmatterWrite{
			RelPath:   assignment.RelPath,
			Mutations: []noteops.FrontmatterMutation{mutation},
		})
	}
	return writes, nil
}

// pinnedNoteKind classifies the note's own `pinned` value from disk, so an
// already-pinned note is recognized without comparing paths against the list.
func pinnedNoteKind(absPath string) (pins.Kind, error) {
	note := &vault.Note{Path: absPath}
	if err := note.Load(); err != nil {
		return pins.NotPinned, noteops.WrapError(noteops.CodeInternal, "pins: read note", err)
	}
	front, err := note.Front()
	if err != nil {
		return pins.NotPinned, noteops.WrapError(noteops.CodeInvalidRequest, "pins: parse note", err)
	}
	raw, present := front.Get(pins.PropertyName)
	_, kind := pins.Classify(raw, present)
	return kind, nil
}

// pinnedNotesPlanError maps a planning failure onto the management error codes.
// A neighbour or target that is no longer pinned, or a pair that no longer
// straddles one gap, means the browser's list is stale, which is a conflict the
// client resolves by refetching.
func pinnedNotesPlanError(err error) error {
	switch {
	case errors.Is(err, pins.ErrNeighborMissing), errors.Is(err, pins.ErrTargetMissing), errors.Is(err, pins.ErrStaleGap):
		return noteops.WrapError(noteops.CodeConflict, "pins: the pinned list changed", err)
	case errors.Is(err, pins.ErrEmptyTarget):
		return noteops.WrapError(noteops.CodeInvalidRequest, "pins: missing note", err)
	}
	return noteops.WrapError(noteops.CodeInvalidRequest, "pins: plan", err)
}

func writePinnedNotesResult(w http.ResponseWriter, message, relPath, journalID string) {
	writeManagementJSON(w, http.StatusOK, noteops.ResultForPath(
		message,
		managementRouteKeyForRelPath(relPath),
		mustManagementNotePath(relPath),
		relPath,
		journalID,
	))
}
