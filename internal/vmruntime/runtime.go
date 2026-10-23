package vmruntime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"foundry/internal/c4verify"
	"foundry/internal/domain"
	"foundry/internal/platformfs"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Request struct{ OperationID, MissionID, Owner, Name, CandidatePath, CandidateDigest string }
type command struct {
	Path      string
	Args, Env []string
	Dir       string
}
type nativeProcess interface {
	Input() io.WriteCloser
	Output() io.ReadCloser
	Wait() error
	Stop() error
	Terminated() bool
}
type Runtime struct {
	mu                       sync.Mutex
	bundleRoot, instanceRoot string
	manifest                 Manifest
	identity                 domain.C4VMIdentity
	production               bool
	launch                   func(context.Context, command) (nativeProcess, error)
	sessions                 map[string]*Session
}
type Session struct {
	mu                       sync.Mutex
	runtime                  *Runtime
	id, dir                  string
	owner                    Request
	wire                     WireRequest
	started, closing, closed bool
	process                  nativeProcess
	cancel                   context.CancelFunc
	done                     chan struct{}
	result                   c4verify.Result
	valid                    bool
	err                      error
}

// Load only reads and verifies installed files; it never probes hardware,
// launches a process, creates an instance or loads receipts as live authority.
func Load(bundleRoot, instanceRoot string) (*Runtime, error) {
	m, id, e := loadManifest(bundleRoot)
	if e != nil {
		return nil, e
	}
	if e := canonicalDirectory(instanceRoot); e != nil {
		return nil, e
	}
	rel, e := filepath.Rel(bundleRoot, instanceRoot)
	if e != nil || rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..") {
		return nil, errors.New("instance root must be outside immutable bundle")
	}
	return &Runtime{bundleRoot: bundleRoot, instanceRoot: instanceRoot, manifest: m, identity: id, production: true, launch: launchNative, sessions: map[string]*Session{}}, nil
}
func (r *Runtime) Identity() domain.C4VMIdentity {
	if r == nil {
		return domain.C4VMIdentity{}
	}
	return r.identity
}
func (r *Runtime) Trusted() bool {
	return r != nil && r.production && r.launch != nil && r.identity.ManifestDigest != ""
}
func (r *Runtime) Lookup(id string) (*Session, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[id]
	return s, ok
}
func randomID() (string, error) {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return "", errors.New("VM randomness unavailable")
	}
	return hex.EncodeToString(b), nil
}
func (r *Runtime) Create(ctx context.Context, q Request) (*Session, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if r == nil || r.launch == nil {
		return nil, errors.New("VM runtime unavailable")
	}
	for _, s := range []string{q.OperationID, q.MissionID, q.Owner, q.Name} {
		if s == "" || len(s) > 256 || strings.ContainsAny(s, "\x00\r\n") {
			return nil, errors.New("invalid VM ownership")
		}
	}
	files, e := snapshot(ctx, q.CandidatePath)
	if e != nil {
		return nil, e
	}
	d, e := SnapshotDigest(files)
	if e != nil || d != q.CandidateDigest {
		return nil, errors.New("candidate digest mismatch")
	}
	return r.create(ctx, q, "verify", files)
}
func (r *Runtime) create(ctx context.Context, q Request, action string, files []SnapshotFile) (*Session, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	id, e := randomID()
	if e != nil {
		return nil, e
	}
	nonce, e := randomID()
	if e != nil {
		return nil, e
	}
	secret, e := randomID()
	if e != nil {
		return nil, e
	}
	if e := canonicalDirectory(r.instanceRoot); e != nil {
		return nil, e
	}
	dir := filepath.Join(r.instanceRoot, id)
	if e := createPrivateDirectory(dir); e != nil {
		return nil, errors.New("cannot create private VM instance")
	}
	s := &Session{runtime: r, id: id, dir: dir, owner: q, wire: WireRequest{SchemaVersion: 1, Action: action, Nonce: nonce, Secret: secret, GuestPlatform: r.identity.GuestPlatform, CandidateDigest: q.CandidateDigest, Files: files}, done: make(chan struct{})}
	if e := s.receipt("allocated"); e != nil {
		return nil, e
	}
	r.mu.Lock()
	r.sessions[id] = s
	r.mu.Unlock()
	return s, nil
}
func (r *Runtime) Probe(ctx context.Context) error {
	if r == nil || r.launch == nil {
		return errors.New("VM runtime unavailable")
	}
	s, e := r.create(ctx, Request{OperationID: "probe", MissionID: "readiness", Owner: "runtime", Name: "probe"}, "probe", []SnapshotFile{})
	if e != nil {
		return e
	}
	e = s.Start(ctx)
	if e == nil {
		e = s.Wait(ctx)
	}
	cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if x := s.Close(cleanup); x != nil {
		return errors.Join(e, x)
	}
	return e
}
func (s *Session) ID() string {
	if s == nil {
		return ""
	}
	return s.id
}
func (s *Session) Matches(q Request) bool {
	return s != nil && s.owner.OperationID == q.OperationID && s.owner.MissionID == q.MissionID && s.owner.Owner == q.Owner && s.owner.Name == q.Name && s.owner.CandidateDigest == q.CandidateDigest
}
func (s *Session) receipt(stage string) error {
	return s.receiptWithNative(stage, nil)
}
func (s *Session) receiptWithNative(stage string, facts *nativeFacts) error {
	b, e := json.Marshal(struct {
		SchemaVersion                                                                   int `json:"schema_version"`
		ID, OperationID, MissionID, Owner, Name, CandidateDigest, ManifestDigest, Stage string
		Native                                                                          *nativeFacts `json:"native,omitempty"`
	}{1, s.id, s.owner.OperationID, s.owner.MissionID, s.owner.Owner, s.owner.Name, s.owner.CandidateDigest, s.runtime.identity.ManifestDigest, stage, facts})
	if e != nil {
		return errors.New("cannot encode instance receipt")
	}
	p := filepath.Join(s.dir, stage+".json")
	f, e := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return errors.New("cannot persist instance receipt")
	}
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil || ce != nil {
		return errors.New("cannot persist instance receipt")
	}
	if e := platformfs.SyncPath(s.dir); e != nil {
		return errors.New("cannot sync instance receipt")
	}
	return nil
}
func (s *Session) Start(ctx context.Context) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	s.mu.Lock()
	if s.started || s.closing || s.closed {
		s.mu.Unlock()
		return errors.New("VM instance already consumed")
	}
	s.started = true
	life, cancel := context.WithTimeout(ctx, 180*time.Second)
	s.cancel = cancel
	s.mu.Unlock()
	cmd, e := s.prepare(life)
	if e != nil {
		cancel()
		s.finish(c4verify.Result{}, false, e)
		return e
	}
	p, e := s.runtime.launch(life, cmd)
	// A failed launch may still own an already-created suspended native child.
	// Publish that exact owner before processing the error; cleanup must never
	// mistake failed admission for proof that no process exists.
	s.mu.Lock()
	s.process = p
	s.mu.Unlock()
	if e != nil || p == nil {
		cancel()
		if p == nil {
			facts := admissionFacts(e)
			failure := withNativeFacts(errors.New("native VM start failed"), facts)
			if receiptErr := s.receiptWithNative("terminal", &facts); receiptErr != nil {
				failure = errors.Join(failure, receiptErr)
			}
			s.finish(c4verify.Result{}, false, failure)
			return failure
		} else {
			go s.failedStart(p)
		}
		return withNativeFacts(errors.New("native VM start failed"), processDiagnostics(p))
	}
	started := make(chan error, 1)
	go func() {
		select {
		case <-life.Done():
			p.Stop()
		case <-s.done:
		}
	}()
	go s.exchange(life, p, started)
	select {
	case e := <-started:
		return e
	case <-life.Done():
		select {
		case <-s.done:
			s.mu.Lock()
			final := s.err
			s.mu.Unlock()
			return final
		default:
		}
		p.Stop()
		return life.Err()
	}
}
func (s *Session) failedStart(p nativeProcess) {
	p.Stop()
	p.Wait()
	p.Input().Close()
	p.Output().Close()
	err := errors.New("native VM start failed")
	if !p.Terminated() {
		err = errors.New("native VM failed-start termination unconfirmed")
	}
	facts := processDiagnostics(p)
	err = withNativeFacts(err, facts)
	if e := s.receiptWithNative("terminal", &facts); e != nil {
		err = errors.Join(err, e)
	}
	s.finish(c4verify.Result{}, false, err)
}
func (s *Session) exchange(ctx context.Context, p nativeProcess, started chan<- error) {
	var result c4verify.Result
	valid := false
	var final error
	startReported := false
	readiness, readinessPrefix := "", ""
	defer func() {
		if final != nil {
			p.Stop()
		}
		waitErr := p.Wait()
		p.Input().Close()
		p.Output().Close()
		if !p.Terminated() {
			final = errors.Join(final, errors.New("native VM termination unconfirmed"))
			valid = false
		} else if waitErr != nil {
			final = errors.Join(final, errors.New("native VM exited unsuccessfully"))
			valid = false
		}
		if ctx.Err() != nil {
			final = errors.Join(final, ctx.Err())
			valid = false
		}
		facts := processDiagnostics(p)
		facts.Readiness, facts.ReadinessPrefix = readiness, readinessPrefix
		final = withNativeFacts(final, facts)
		if e := s.receiptWithNative("terminal", &facts); e != nil {
			final = errors.Join(final, e)
			valid = false
		}
		s.finish(result, valid, final)
		if !startReported {
			started <- final
		}
	}()
	marker := make([]byte, len(ReadyMarker))
	_, readErr := io.ReadFull(p.Output(), marker)
	validMarker := readErr == nil && string(marker) == ReadyMarker
	switch {
	case validMarker:
		readiness = "matched"
	case ctx.Err() == context.DeadlineExceeded:
		readiness = "deadline"
	case ctx.Err() != nil:
		readiness = "canceled"
	case readErr == io.EOF:
		readiness = "eof"
	case readErr == io.ErrUnexpectedEOF:
		readiness = "short_read"
	case readErr != nil:
		readiness = "read_failed"
	case string(marker) != ReadyMarker:
		readiness, readinessPrefix = "marker_mismatch", "unknown"
		// Classify only the already-read fixed public prefixes. Never scan past a
		// rejected marker, accept CRLF, or retain arbitrary guest/host output.
		if string(marker) == "WHPX: Failed to quer" {
			readinessPrefix = "whpx_clock_query_prefix"
		}
		if string(marker) == "FOUNDRY_VM_READY_V1\r" {
			readinessPrefix = "ready_crlf"
		}
	default:
		readiness = "matched"
	}
	clear(marker)
	if !validMarker {
		final = errors.New("trusted guest readiness missing")
		return
	}
	if e := WriteRequest(p.Input(), s.wire); e != nil {
		final = errors.New("guest input transfer failed")
		return
	}
	if e := s.receipt("started"); e != nil {
		final = e
		return
	}
	startReported = true
	started <- nil
	response, e := ReadResponse(p.Output(), s.wire)
	if e != nil {
		final = e
		return
	}
	var extra [1]byte
	n, e := p.Output().Read(extra[:])
	if n != 0 || e != io.EOF {
		final = errors.New("duplicate or trailing guest frame")
		return
	}
	result = response.Result
	valid = true
}
func (s *Session) finish(result c4verify.Result, valid bool, e error) {
	s.mu.Lock()
	s.result = result
	s.valid = valid
	s.err = e
	close(s.done)
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
}
func (s *Session) Wait(ctx context.Context) error {
	s.mu.Lock()
	started := s.started
	s.mu.Unlock()
	if !started {
		return errors.New("VM not started")
	}
	select {
	case <-s.done:
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.err
	case <-ctx.Done():
		s.mu.Lock()
		cancel := s.cancel
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		return ctx.Err()
	}
}
func (s *Session) Result() (c4verify.Result, bool) {
	if s == nil {
		return c4verify.Result{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.done:
		return s.result, s.valid
	default:
		return c4verify.Result{}, false
	}
}
func (s *Session) Running() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		return false
	}
	select {
	case <-s.done:
		return s.process != nil && !s.process.Terminated()
	default:
		return true
	}
}
func (s *Session) Close(ctx context.Context) error {
	ctx, closeCancel := context.WithTimeout(ctx, 5*time.Second)
	defer closeCancel()
	if e := ctx.Err(); e != nil {
		return e
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	// Reserve the consumed state while still holding the initial lock. Failed
	// persistence may be retried, but no new Start can race this cleanup.
	s.closing = true
	started := s.started
	cancel, p := s.cancel, s.process
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if p != nil {
		// A termination request can fail after the child already exits. It is
		// observation of the retained handle, never this return code, that
		// establishes cleanup. Keep uncertainty until that observation below.
		p.Stop()
	}
	if started {
		select {
		case <-s.done:
		case <-ctx.Done():
			return errors.New("owned VM termination unconfirmed")
		}
	}
	s.mu.Lock()
	p = s.process
	s.mu.Unlock()
	if p != nil && !p.Terminated() {
		if observer, ok := p.(interface{ observeTermination(context.Context) error }); ok {
			if e := observer.observeTermination(ctx); e != nil {
				return errors.New("owned VM termination unconfirmed")
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	if s.process != nil && !s.process.Terminated() {
		return errors.New("owned VM termination unconfirmed")
	}
	if e := s.receipt("closed"); e != nil {
		return e
	}
	s.closed = true
	s.wire.Secret = ""
	s.wire.Files = nil
	return nil
}

func snapshot(ctx context.Context, dir string) ([]SnapshotFile, error) {
	if e := canonicalDirectory(dir); e != nil {
		return nil, e
	}
	root, e := os.OpenRoot(dir)
	if e != nil {
		return nil, errors.New("cannot open candidate")
	}
	defer root.Close()
	files := []SnapshotFile{}
	total, entries := 0, 0
	e = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		entries++
		if entries > 2000 {
			return errors.New("candidate entry bound exceeded")
		}
		if p == "." {
			return nil
		}
		if !safeRelative(p) || d.Type()&os.ModeSymlink != 0 {
			return errors.New("unsafe candidate path")
		}
		if d.IsDir() {
			return nil
		}
		if len(files) >= 500 {
			return errors.New("candidate file count exceeded")
		}
		f, e := openAsset(root, p)
		if e != nil {
			return e
		}
		defer f.Close()
		info, e := f.Stat()
		if e != nil {
			return e
		}
		mode, e := platformfs.CanonicalMode(info.Mode())
		if e != nil {
			return e
		}
		b, e := io.ReadAll(io.LimitReader(f, int64(MaxSnapshotBytes-total)+1))
		if e != nil {
			return e
		}
		total += len(b)
		if total > MaxSnapshotBytes {
			return errors.New("candidate exceeds bound")
		}
		files = append(files, SnapshotFile{Path: p, Mode: uint32(mode.Perm()), Data: b})
		return nil
	})
	if e != nil {
		return nil, e
	}
	return files, nil
}
func (s *Session) prepare(ctx context.Context) (command, error) {
	m, id, e := loadManifest(s.runtime.bundleRoot)
	if e != nil || id != s.runtime.identity {
		return command{}, errors.New("installed VM bundle changed")
	}
	root, e := os.OpenRoot(s.runtime.bundleRoot)
	if e != nil {
		return command{}, errors.New("cannot open VM bundle")
	}
	defer root.Close()
	dest := filepath.Join(s.dir, "assets")
	if e := os.Mkdir(dest, 0700); e != nil {
		return command{}, errors.New("cannot stage immutable VM assets")
	}
	for _, a := range m.Assets {
		if e := ctx.Err(); e != nil {
			return command{}, e
		}
		// Legal/source inventories and the separately indexed verifier remain
		// verified above, but are not runtime dependencies. Stage only boot
		// files and the audited helper/QEMU dependency and firmware closure.
		if a.Path != m.Kernel && a.Path != m.RootFS && !strings.HasPrefix(a.Path, "runtime/") {
			continue
		}
		p := filepath.Join(dest, filepath.FromSlash(a.Path))
		if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
			return command{}, errors.New("cannot stage VM asset")
		}
		f, e := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0400)
		if e != nil {
			return command{}, errors.New("cannot stage VM asset")
		}
		e = verifyAsset(root, a, f)
		ce := f.Close()
		if e != nil || ce != nil {
			return command{}, errors.New("VM asset staging integrity failed")
		}
		if a.Path == m.Runtime {
			if e := os.Chmod(p, 0500); e != nil {
				return command{}, errors.New("cannot secure VM helper")
			}
		}
	}
	return nativeCommand(dest, m), nil
}
