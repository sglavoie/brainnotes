package vaultsync

import (
	"fmt"
	"path"
)

// sha256HexLen is the length of a hex-encoded SHA-256 digest.
const sha256HexLen = 64

// Validate reports whether the manifest describes a package at all.
//
// It is the format check and nothing else: it says the manifest is
// self-consistent, that every path it names is portable vault content, and that
// its inventory covers its changes. It says nothing about whether this vault
// should accept the package — that is CheckEnrollment — and nothing about
// whether the bytes arrived — that is VerifyPayloads. The three are separate
// because their remedies are: a malformed package is discarded, a misaddressed
// one is somebody else's, and an incomplete one is waited on.
//
// The format token is checked first and on its own. A manifest from a format
// this build does not know may mean anything at all by the fields it shares
// with this one, so nothing else here is entitled to an opinion about it.
func (m *Manifest) Validate() error {
	if m == nil {
		return fmt.Errorf("%w: no manifest", ErrMalformedPackage)
	}
	if m.Format != Format {
		return fmt.Errorf("%w: got %q want %q", ErrUnsupportedFormat, m.Format, Format)
	}
	if err := m.validateHeader(); err != nil {
		return err
	}
	if err := m.validatePayloads(); err != nil {
		return err
	}
	return m.validateOperations()
}

func (m *Manifest) validateHeader() error {
	if err := checkIdentity(ErrMalformedPackage, "package_id", packagePrefix, m.PackageID); err != nil {
		return err
	}
	if err := checkIdentity(ErrMalformedPackage, "vault_id", vaultPrefix, m.VaultID); err != nil {
		return err
	}
	if err := checkIdentity(ErrMalformedPackage, "origin_device", devicePrefix, m.OriginDevice); err != nil {
		return err
	}
	if m.Epoch == 0 {
		return fmt.Errorf("%w: epoch must be at least 1", ErrMalformedPackage)
	}
	if m.Sequence == 0 {
		return fmt.Errorf("%w: sequence must be at least 1", ErrMalformedPackage)
	}
	if m.CreatedAt.IsZero() {
		return fmt.Errorf("%w: created_at is unset", ErrMalformedPackage)
	}
	return m.validateRequires()
}

func (m *Manifest) validateRequires() error {
	for i, dep := range m.Requires {
		field := fmt.Sprintf("requires[%d].origin_device", i)
		if err := checkIdentity(ErrMalformedPackage, field, devicePrefix, dep.OriginDevice); err != nil {
			return err
		}
		if dep.Sequence == 0 {
			return fmt.Errorf("%w: requires[%d] sequence must be at least 1", ErrMalformedPackage, i)
		}
		// A package cannot depend on itself or on anything its own origin
		// publishes later: its own stream is already totally ordered by
		// Sequence, and a forward reference there would never be satisfiable.
		if dep.OriginDevice == m.OriginDevice && dep.Sequence >= m.Sequence {
			return fmt.Errorf(
				"%w: requires[%d] names its own origin at sequence %d, which is not earlier than %d",
				ErrMalformedPackage, i, dep.Sequence, m.Sequence,
			)
		}
	}
	return nil
}

// validatePayloads checks the inventory's own shape. Whether the named bytes
// are actually present is VerifyPayloads' question.
func (m *Manifest) validatePayloads() error {
	seen := make(map[string]struct{}, len(m.Payloads))
	for i, payload := range m.Payloads {
		if err := validPayloadName(payload.Name); err != nil {
			return fmt.Errorf("%w: payloads[%d]: %v", ErrMalformedPackage, i, err)
		}
		if _, duplicate := seen[payload.Name]; duplicate {
			return fmt.Errorf("%w: payload %q is listed twice", ErrMalformedPackage, payload.Name)
		}
		seen[payload.Name] = struct{}{}
		if !validHexDigest(payload.SHA256) {
			return fmt.Errorf("%w: payload %q has no sha256", ErrMalformedPackage, payload.Name)
		}
		if payload.Size < 0 {
			return fmt.Errorf("%w: payload %q has a negative size", ErrMalformedPackage, payload.Name)
		}
	}
	return nil
}

// validPayloadName reports whether name is a usable entry in the payload
// directory. A payload name is a single component with no directory structure
// at all, which is what makes "write the payload" incapable of writing anywhere
// but inside the package — the containment is in the format rather than in
// each reader's care.
func validPayloadName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("name is empty")
	case name == "." || name == "..":
		return fmt.Errorf("name %q is a directory reference", name)
	case Classify(name) != ClassPortable:
		return fmt.Errorf("name %q is not a usable file name", name)
	}
	for _, r := range name {
		if r == '/' || r == '\\' || r == 0 {
			return fmt.Errorf("name %q is a path rather than a file name", name)
		}
	}
	return nil
}

func validHexDigest(s string) bool {
	if len(s) != sha256HexLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') {
			continue
		}
		return false
	}
	return true
}

// validateOperations walks the operation boundaries, then the changes inside
// them, accumulating the two package-wide facts: operation identities are
// unique, and no two paths collide.
func (m *Manifest) validateOperations() error {
	if len(m.Operations) == 0 {
		return fmt.Errorf("%w: package changes nothing", ErrMalformedPackage)
	}
	state := newManifestScan(m)
	for i := range m.Operations {
		if err := state.operation(i, &m.Operations[i]); err != nil {
			return err
		}
	}
	return state.unreferencedPayload()
}

// manifestScan carries the package-wide state a per-change check needs.
type manifestScan struct {
	payloads    map[string]Payload
	order       []string
	operations  map[string]struct{}
	opIndex     map[string]int
	collisions  map[string]string
	directories map[string]string
	referenced  map[string]struct{}
}

func newManifestScan(m *Manifest) *manifestScan {
	order := make([]string, 0, len(m.Payloads))
	for _, payload := range m.Payloads {
		order = append(order, payload.Name)
	}
	// Built up front rather than as the scan walks, because a predecessor may
	// name an operation the scan has not reached yet and the whole point of the
	// check is to refuse exactly that.
	opIndex := make(map[string]int, len(m.Operations))
	for i, op := range m.Operations {
		opIndex[op.OperationID] = i
	}
	return &manifestScan{
		payloads:    m.payloadIndex(),
		order:       order,
		operations:  map[string]struct{}{},
		opIndex:     opIndex,
		collisions:  map[string]string{},
		directories: map[string]string{},
		referenced:  map[string]struct{}{},
	}
}

func (s *manifestScan) operation(index int, op *Operation) error {
	if err := checkIdentity(ErrMalformedPackage, "operation_id", operationPrefix, op.OperationID); err != nil {
		return err
	}
	if _, duplicate := s.operations[op.OperationID]; duplicate {
		return fmt.Errorf("%w: operation %q appears twice", ErrMalformedPackage, op.OperationID)
	}
	s.operations[op.OperationID] = struct{}{}
	if op.RecordedAt.IsZero() {
		return fmt.Errorf("%w: operation %q has no recorded_at", ErrMalformedPackage, op.OperationID)
	}
	if len(op.Changes) == 0 {
		return fmt.Errorf("%w: operation %q changes nothing", ErrMalformedPackage, op.OperationID)
	}
	for _, change := range op.Changes {
		if err := s.change(index, op.OperationID, change); err != nil {
			return err
		}
	}
	return nil
}

func (s *manifestScan) change(index int, operationID string, change Change) error {
	if err := classifyError(change.Path); err != nil {
		return fmt.Errorf("%w: %q", err, change.Path)
	}
	if err := s.collision(change.Path); err != nil {
		return err
	}
	if err := s.predecessors(index, operationID, change); err != nil {
		return err
	}
	if err := checkExpectation(change); err != nil {
		return err
	}
	return s.result(change)
}

// collision records a path and refuses one that names the same file as a path
// already in the package, or the same name as a directory another path in the
// package needs.
//
// The second half is what a map keyed on whole paths alone misses. `Archive` and
// `Archive/2024.md` are two distinct keys and one impossible request: whichever
// the applier lands first makes the other one fail, because a name is a file or
// a directory and not both. The failure arrives halfway through applying a
// package, which is the one place this protocol has no good answer. On the
// case-insensitive replica it does not even have to look alike — `archive` and
// `Archive/2024.md` are the same clash.
func (s *manifestScan) collision(p string) error {
	key := CollisionKey(p)
	if first, seen := s.collisions[key]; seen {
		if first == p {
			return fmt.Errorf("%w: %q is changed twice in one package", ErrPathCollision, p)
		}
		return fmt.Errorf("%w: %q and %q are one file on this vault", ErrPathCollision, first, p)
	}
	if owner, isDir := s.directories[key]; isDir {
		return fmt.Errorf("%w: %q is a file here and a directory of %q", ErrPathCollision, p, owner)
	}
	s.collisions[key] = p
	return s.claimAncestors(p)
}

// claimAncestors records every directory p needs to exist and refuses one that
// another change in this package writes as a file.
//
// The walk stops at the first ancestor already claimed: whatever claimed it
// walked the rest of the chain above it at the time, so there is nothing left to
// record and nothing left to check.
func (s *manifestScan) claimAncestors(p string) error {
	for dir := path.Dir(p); dir != "."; dir = path.Dir(dir) {
		key := CollisionKey(dir)
		if file, seen := s.collisions[key]; seen {
			return fmt.Errorf("%w: %q is a file here and a directory of %q", ErrPathCollision, file, p)
		}
		if _, claimed := s.directories[key]; claimed {
			return nil
		}
		s.directories[key] = p
	}
	return nil
}

// predecessors checks one change's causal edges.
//
// Beyond their shape, one thing is refused here that takes the whole manifest to
// see: an edge naming an operation in *this* package at the same position or a
// later one. A manifest's operations are ordered and an applier walks them in
// that order, so a forward edge is one nothing can ever satisfy — it either
// stalls an applier that waits for its predecessors or is quietly dropped by one
// that does not, and "quietly dropped" is a lost ordering constraint rather than
// an error anybody sees. A *backward* edge is legitimate and load-bearing: it is
// how a multi-file operation boundary survives being split into several
// operations, with the repair naming the rename it repairs.
func (s *manifestScan) predecessors(index int, operationID string, change Change) error {
	seen := make(map[string]struct{}, len(change.Predecessors))
	for _, predecessor := range change.Predecessors {
		field := fmt.Sprintf("predecessor of %q", change.Path)
		if err := checkIdentity(ErrMalformedPackage, field, operationPrefix, predecessor); err != nil {
			return err
		}
		if _, duplicate := seen[predecessor]; duplicate {
			return fmt.Errorf("%w: %q lists predecessor %q twice", ErrMalformedPackage, change.Path, predecessor)
		}
		seen[predecessor] = struct{}{}
		if err := s.predecessorOrder(index, operationID, change.Path, predecessor); err != nil {
			return err
		}
	}
	return nil
}

func (s *manifestScan) predecessorOrder(index int, operationID, changePath, predecessor string) error {
	if predecessor == operationID {
		return fmt.Errorf("%w: operation %q is its own predecessor at %q", ErrMalformedPackage, operationID, changePath)
	}
	if at, inPackage := s.opIndex[predecessor]; inPackage && at > index {
		return fmt.Errorf(
			"%w: %q depends on operation %q, which this package orders after it",
			ErrMalformedPackage, changePath, predecessor,
		)
	}
	return nil
}

// checkExpectation enforces that the expected prior state is stated exactly
// once: a hash when and only when the origin believed the path existed. A
// change carrying both "did not exist" and a hash, or "existed" and none, would
// leave the receiver choosing which half to believe.
func checkExpectation(change Change) error {
	if change.ExpectExists {
		if !validHexDigest(change.ExpectSHA256) {
			return fmt.Errorf("%w: %q expects an existing file with no expect_sha256", ErrMalformedPackage, change.Path)
		}
		return nil
	}
	if change.ExpectSHA256 != "" {
		return fmt.Errorf("%w: %q expects no file yet carries expect_sha256", ErrMalformedPackage, change.Path)
	}
	return nil
}

// result enforces the two shapes a change may have: a write that names bytes
// the inventory carries, or a tombstone that names none.
func (s *manifestScan) result(change Change) error {
	switch change.Action {
	case ActionWrite:
		return s.write(change)
	case ActionDelete:
		return tombstone(change)
	default:
		return fmt.Errorf("%w: %q has unknown action %q", ErrMalformedPackage, change.Path, change.Action)
	}
}

func (s *manifestScan) write(change Change) error {
	payload, listed := s.payloads[change.Payload]
	if !listed {
		return fmt.Errorf("%w: %q names payload %q, which the inventory does not list", ErrMalformedPackage, change.Path, change.Payload)
	}
	s.referenced[change.Payload] = struct{}{}
	if change.ResultSHA256 != payload.SHA256 {
		return fmt.Errorf("%w: %q expects payload %q to hash to %q", ErrMalformedPackage, change.Path, change.Payload, change.ResultSHA256)
	}
	if change.ResultSize != payload.Size {
		return fmt.Errorf("%w: %q expects payload %q to be %d bytes", ErrMalformedPackage, change.Path, change.Payload, change.ResultSize)
	}
	return nil
}

func tombstone(change Change) error {
	if change.Payload != "" {
		return fmt.Errorf("%w: tombstone for %q carries payload %q", ErrMalformedPackage, change.Path, change.Payload)
	}
	if change.ResultSHA256 != "" || change.ResultSize != 0 {
		return fmt.Errorf("%w: tombstone for %q carries resulting content", ErrMalformedPackage, change.Path)
	}
	// A delete must name what it is deleting. checkExpectation has already made
	// ExpectExists and ExpectSHA256 agree, so requiring the flag here requires
	// the hash with it — and that hash is the only thing an applier can compare
	// the live file against before removing it. A tombstone that expects no file
	// is a delete with nothing to order against: it removes whatever happens to
	// be at the path now, which on the other replica may be a note written since
	// the package was built, and a receipt would then acknowledge destroying it.
	if !change.ExpectExists {
		return fmt.Errorf("%w: tombstone for %q expects no file to delete", ErrMalformedPackage, change.Path)
	}
	return nil
}

// unreferencedPayload refuses an inventory entry no change names. A package
// carries exactly the bytes its changes need; anything else is content that
// would cross the boundary without a single line of the manifest saying what it
// is for.
func (s *manifestScan) unreferencedPayload() error {
	for _, name := range s.order {
		if _, used := s.referenced[name]; !used {
			return fmt.Errorf("%w: payload %q is carried but no change names it", ErrMalformedPackage, name)
		}
	}
	return nil
}
