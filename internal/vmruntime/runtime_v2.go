package vmruntime

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"foundry/internal/c4verify"
	"foundry/internal/dependencybundle"
	"foundry/internal/domain"
	"io"
	"reflect"
	"slices"
)

type RequestV2 struct {
	Owner               Request
	Profile             domain.C4Profile
	Inputs              domain.C4InputContractV2
	DependencyStoreRoot string // Host-private location; never wire/receipt identity.
}

// sessionV2 owns immutable admitted material. Native launch/wait/cleanup remain
// exclusively in Session; this payload selects only codecs and evidence.
type sessionV2 struct {
	wire                WireRequestV2
	source              []SnapshotFile
	dependencyStoreRoot string
	requestDigest       string
}

func (r *Runtime) availableV2() bool {
	return r != nil && r.launch != nil && r.identity.SchemaVersion == 2 && r.identity.Validate() == nil && r.manifestV2.RuntimeInventoryDigest != ""
}

// ProbeTargetV2 constructs metadata only, without touching project/store paths
// or launching a process. Create/Probe/Start revalidate the installed bundle.
func (r *Runtime) ProbeTargetV2(profileID string) (ProbeTargetV2, error) {
	if !r.availableV2() {
		return ProbeTargetV2{}, errors.New("VM v2 runtime unavailable")
	}
	if _, e := domain.FixedC4VMPolicyV2(profileID); e != nil {
		return ProbeTargetV2{}, ErrProtocolV2
	}
	return ProbeTargetV2{profileID, r.identity, r.manifestV2.RuntimeInventoryDigest}, nil
}
func (r *Runtime) revalidateV2() error {
	if !r.availableV2() {
		return errors.New("VM v2 runtime unavailable")
	}
	m, id, d, e := loadManifestV2(r.bundleRoot)
	if e != nil || id != r.identity || m.RuntimeInventoryDigest != r.manifestV2.RuntimeInventoryDigest || !reflect.DeepEqual(d, r.inventoryV2) {
		return errors.New("installed VM bundle changed")
	}
	return nil
}
func cloneRequestV2(q RequestV2) RequestV2 {
	if q.Profile.EmbeddedVM != nil {
		v := *q.Profile.EmbeddedVM
		q.Profile.EmbeddedVM = &v
	}
	if q.Profile.V2 != nil {
		v := *q.Profile.V2
		q.Profile.V2 = &v
	}
	q.Inputs.SourceFiles = slices.Clone(q.Inputs.SourceFiles)
	q.Inputs.Protected = slices.Clone(q.Inputs.Protected)
	q.Inputs.ProtectedPrefixes = slices.Clone(q.Inputs.ProtectedPrefixes)
	q.Inputs.TestFiles = slices.Clone(q.Inputs.TestFiles)
	q.Inputs.SourceRoots = slices.Clone(q.Inputs.SourceRoots)
	if q.Inputs.TypeScript != nil {
		v := *q.Inputs.TypeScript
		v.InputFiles = slices.Clone(v.InputFiles)
		q.Inputs.TypeScript = &v
	}
	return q
}
func (r *Runtime) validateRequestV2(q RequestV2) (ProbeTargetV2, error) {
	target, e := r.ProbeTargetV2(q.Profile.ID)
	if e != nil {
		return ProbeTargetV2{}, e
	}
	if q.Profile.SchemaVersion != 2 || q.Inputs.ValidateProfileBinding(q.Profile) != nil || *q.Profile.EmbeddedVM != target.Identity || q.Profile.V2.RuntimeInventoryDigest != target.RuntimeInventoryDigest || !hexDigest(q.Owner.CandidateDigest) {
		return ProbeTargetV2{}, ErrProtocolV2
	}
	if _, e := OwnerDigestV2(q.Owner); e != nil {
		return ProbeTargetV2{}, e
	}
	return target, nil
}
func (r *Runtime) CreateV2(ctx context.Context, q RequestV2) (*Session, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	target, e := r.validateRequestV2(q)
	if e != nil {
		return nil, e
	}
	q = cloneRequestV2(q)
	if e = r.revalidateV2(); e != nil {
		return nil, e
	}
	source, e := snapshot(ctx, q.Owner.CandidatePath)
	if e != nil {
		return nil, e
	}
	digest, e := SnapshotDigest(source)
	if e != nil || digest != q.Owner.CandidateDigest {
		return nil, errors.New("candidate digest mismatch")
	}
	// WalkDir preorder is not complete-path lexical order (src/a vs src.txt).
	slices.SortFunc(source, func(a, b SnapshotFile) int { return cmp.Compare(a.Path, b.Path) })
	metadata, e := SourceMetadataV2(source)
	if e != nil {
		return nil, e
	}
	manifest, e := dependencybundle.DescribeVerified(ctx, q.DependencyStoreRoot, dependencybundle.Binding(q.Inputs.Dependencies))
	if e != nil {
		return nil, errors.New("VM dependency admission failed")
	}
	owner, _ := OwnerDigestV2(q.Owner)
	policy, _ := PolicyDigestV2(q.Profile.ID)
	verify := &VerifyRequestV2{OwnerDigest: owner, Profile: q.Profile, ProfileDigest: q.Profile.ProfileDigest(), PolicyDigest: policy, Inputs: q.Inputs, CandidateDigest: digest, SourceFiles: metadata, DependencyManifest: manifest}
	return r.createV2(ctx, q.Owner, WireRequestV2{SchemaVersion: 2, Action: "verify", Target: target, Verify: verify}, source, q.DependencyStoreRoot)
}
func (r *Runtime) createV2(ctx context.Context, owner Request, wire WireRequestV2, source []SnapshotFile, store string) (*Session, error) {
	var e error
	wire.Nonce, e = randomID()
	if e != nil {
		return nil, e
	}
	wire.Secret, e = randomID()
	if e != nil {
		return nil, e
	}
	digest, e := RequestDigestV2(wire)
	if e != nil {
		return nil, e
	}
	return r.allocateSession(ctx, owner, WireRequest{}, &sessionV2{wire: wire, source: source, dependencyStoreRoot: store, requestDigest: digest})
}
func (r *Runtime) ProbeV2(ctx context.Context, target ProbeTargetV2) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	expected, e := r.ProbeTargetV2(target.ProfileID)
	if e != nil || target != expected {
		return ErrProtocolV2
	}
	if e = r.revalidateV2(); e != nil {
		return e
	}
	s, e := r.createV2(ctx, Request{OperationID: "probe", MissionID: "readiness", Owner: "runtime", Name: "probe"}, WireRequestV2{SchemaVersion: 2, Action: "probe", Target: target}, nil, "")
	if e != nil {
		return e
	}
	return runProbe(ctx, s)
}

// MatchesV2 compares content authority, never candidate/store locations. It is
// metadata-only; transfer still reopens and verifies the private admitted store.
func (s *Session) MatchesV2(q RequestV2) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.v2 == nil || s.v2.wire.Action != "verify" {
		return false
	}
	target, e := s.runtime.validateRequestV2(q)
	if e != nil {
		return false
	}
	v := s.v2.wire.Verify
	owner, _ := OwnerDigestV2(q.Owner)
	policy, _ := PolicyDigestV2(q.Profile.ID)
	return target == s.v2.wire.Target && owner == v.OwnerDigest && policy == v.PolicyDigest && q.Owner.CandidateDigest == v.CandidateDigest && reflect.DeepEqual(q.Profile, v.Profile) && reflect.DeepEqual(q.Inputs, v.Inputs)
}
func (s *Session) readyMarker() string {
	if s.v2 != nil {
		return ReadyMarkerV2
	}
	return ReadyMarker
}
func (s *Session) writeRequest(ctx context.Context, w io.Writer) error {
	if s.v2 != nil {
		return WriteRequestV2(ctx, w, s.v2.wire, s.v2.source, s.v2.dependencyStoreRoot)
	}
	return WriteRequest(w, s.wire)
}
func (s *Session) readResponse(r io.Reader) (c4verify.Result, error) {
	if s.v2 != nil {
		v, e := ReadResponseV2(r, s.v2.wire)
		return v.Result, e
	}
	v, e := ReadResponse(r, s.wire)
	return v.Result, e
}
func (s *Session) erasePayload() {
	s.wire.Secret = ""
	s.wire.Files = nil
	if s.v2 != nil {
		s.v2.wire.Secret = ""
		for i := range s.v2.source {
			clear(s.v2.source[i].Data)
		}
		s.v2.source = nil
		s.v2.dependencyStoreRoot = ""
	}
}

type receiptV2 struct {
	SchemaVersion    int           `json:"schema_version"`
	ID               string        `json:"id"`
	Stage            string        `json:"stage"`
	OperationID      string        `json:"operation_id"`
	MissionID        string        `json:"mission_id"`
	Owner            string        `json:"owner"`
	Name             string        `json:"name"`
	Target           ProbeTargetV2 `json:"target"`
	ProfileDigest    string        `json:"profile_digest"`
	PolicyDigest     string        `json:"policy_digest"`
	InputDigest      string        `json:"input_digest"`
	DependencyDigest string        `json:"dependency_digest"`
	CandidateDigest  string        `json:"candidate_digest"`
	RequestDigest    string        `json:"request_digest"`
	Native           *nativeFacts  `json:"native,omitempty"`
}

func (s *Session) receiptBytesV2(stage string, facts *nativeFacts) ([]byte, error) {
	p := s.v2
	policy, e := PolicyDigestV2(p.wire.Target.ProfileID)
	if e != nil {
		return nil, e
	}
	r := receiptV2{SchemaVersion: 2, ID: s.id, Stage: stage, OperationID: s.owner.OperationID, MissionID: s.owner.MissionID, Owner: s.owner.Owner, Name: s.owner.Name, Target: p.wire.Target, PolicyDigest: policy, RequestDigest: p.requestDigest, Native: facts}
	if v := p.wire.Verify; v != nil {
		r.ProfileDigest = v.ProfileDigest
		r.InputDigest = v.Inputs.ExecutionContractDigest
		r.DependencyDigest = v.Inputs.Dependencies.BundleDigest
		r.CandidateDigest = v.CandidateDigest
	}
	return json.Marshal(r)
}
