package plugins

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"
	"strings"
	"sync"
)

const MaterialContract = "material/v1"
const MaterialFrameBytes = 256 << 10

type MaterialDirection string

const (
	MaterialToWorker MaterialDirection = "to_worker"
	MaterialToHost   MaterialDirection = "to_host"
)

// MaterialSpec is not an authority token. Only IssueMaterial in a real host
// callback context can bind it to this invocation and installed generation.
type MaterialSpec struct {
	Capability string            `json:"capability"`
	Direction  MaterialDirection `json:"direction"`
	Kind       string            `json:"kind"`
	Total      int64             `json:"total"`
	SHA256     string            `json:"sha256"`
}

type MaterialGrant struct {
	Contract   string `json:"contract"`
	Token      string `json:"token"`
	Invocation string `json:"invocation"`
	ChunkBytes int    `json:"chunk_bytes"`
	MaterialSpec
}

// MaterialPort contains only trusted host callbacks. It never crosses the wire,
// accepts a worker pathname, or changes permissions. Read must return exactly n
// bytes or an error. Write must finish this one offset before returning. Close
// revokes/joins only resources owned by this grant; it must be idempotent.
// Successful Finish does not close an upload: the ordinary submit still claims
// the verified material once. Neither a failed stream nor revoke revives it.
type MaterialPort struct {
	Read  func(context.Context, int64, int) ([]byte, error)
	Write func(context.Context, int64, []byte) error
	Close func() error
}

type materialGrantState struct {
	mu       sync.Mutex
	grant    MaterialGrant
	port     MaterialPort
	guard    func(context.Context) error
	next     int64
	digest   hash.Hash
	finished bool
	failed   bool
	closed   bool
}

type materialHostScope struct {
	ctx          context.Context
	cancel       context.CancelFunc
	binding      SessionBinding
	invocation   string
	maxFrame     int
	capabilities map[string]bool
	guard        func(context.Context) error
	mu           sync.Mutex
	closed       bool
	closeDone    chan struct{}
	closeErr     error
	grants       map[string]*materialGrantState
}

type materialIssueKey struct{}
type materialIssueTicket struct {
	scope      *materialHostScope
	capability string
	mu         sync.Mutex
	kinds      map[string]bool
	active     bool
}

func validMaterialSpec(s MaterialSpec) bool {
	if s.Total < 0 || len(s.SHA256) != 64 || strings.ToLower(s.SHA256) != s.SHA256 {
		return false
	}
	if raw, err := hex.DecodeString(s.SHA256); err != nil || len(raw) != sha256.Size {
		return false
	}
	switch s.Capability {
	case LocalTorrentBackupCapability:
		return s.Direction == MaterialToWorker && s.Kind == "bytes"
	case DownloadTorrentCapability:
		return s.Direction == MaterialToWorker && (s.Kind == "bytes" || s.Kind == "result_record")
	case QBittorrentAddCapability, TransmissionAddCapability:
		return s.Direction == MaterialToHost && s.Kind == "bytes"
	}
	return false
}

// newMaterialHostScope and hostContext are framework-only seams. The lifecycle
// glue must create one scope per actual call after material/v1 negotiation,
// revoke it before joining the call, and issue a fresh ticket for each admitted
// ordinary callback. Creating a Go context or sending a grant-shaped DTO is not
// a way for a worker to manufacture this private ticket on the host.
func newMaterialHostScope(ctx context.Context, binding SessionBinding, invocation string, maxFrame int, capabilities []string, guard func(context.Context) error) (*materialHostScope, error) {
	if ctx == nil || ctx.Err() != nil || binding.Generation < 1 || !ValidInstanceID(binding.InstanceID) || !identityPattern.MatchString(binding.PluginID) || !versionPattern.MatchString(binding.Version) || !identityPattern.MatchString(invocation) || strings.Contains(invocation, ".") || len(invocation) > 63 || maxFrame < 256 || maxFrame > 1<<20 || guard == nil {
		return nil, failure("invalid_material_scope")
	}
	life, cancel := context.WithCancel(ctx)
	s := &materialHostScope{ctx: life, cancel: cancel, binding: binding, invocation: invocation, maxFrame: maxFrame, capabilities: map[string]bool{}, guard: guard, closeDone: make(chan struct{}), grants: map[string]*materialGrantState{}}
	for _, capability := range capabilities {
		s.capabilities[capability] = true
	}
	if err := s.check(ctx); err != nil {
		cancel()
		return nil, err
	}
	return s, nil
}

func (s *materialHostScope) check(ctx context.Context) error {
	if ctx == nil {
		return failure("invalid_material_scope")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return failure("material_scope_closed")
	}
	return s.guard(ctx)
}

func (s *materialHostScope) hostContext(ctx context.Context, capability string) (context.Context, func(), error) {
	if err := s.check(ctx); err != nil {
		return nil, nil, err
	}
	if !s.capabilities[capability] {
		return nil, nil, failure("material_capability_denied")
	}
	ticket := &materialIssueTicket{scope: s, capability: capability, kinds: map[string]bool{}, active: true}
	end := func() { ticket.mu.Lock(); ticket.active = false; ticket.mu.Unlock() }
	return context.WithValue(ctx, materialIssueKey{}, ticket), end, nil
}

// IssueMaterial is usable only by an already-admitted host callback. Its guard
// is the consumer's immutable endpoint/path/owner snapshot check, in addition
// to the framework call guard. The ticket allows one bytes grant and (only for
// S12) one result_record grant, not an unbounded stream-factory callback.
func IssueMaterial(ctx context.Context, spec MaterialSpec, port MaterialPort, guard func(context.Context) error) (MaterialGrant, error) {
	var zero MaterialGrant
	if ctx == nil {
		return zero, failure("material_issue_denied")
	}
	ticket, ok := ctx.Value(materialIssueKey{}).(*materialIssueTicket)
	if !ok || ticket == nil || ticket.scope == nil || guard == nil || ticket.capability != spec.Capability || !validMaterialSpec(spec) || port.Close == nil {
		return zero, failure("material_issue_denied")
	}
	if spec.Direction == MaterialToWorker && (port.Read == nil || port.Write != nil) || spec.Direction == MaterialToHost && (port.Write == nil || port.Read != nil) {
		return zero, failure("invalid_material_port")
	}
	s := ticket.scope
	if err := s.check(ctx); err != nil {
		return zero, err
	}
	if err := guard(ctx); err != nil {
		return zero, err
	}
	ticket.mu.Lock()
	defer ticket.mu.Unlock()
	if !ticket.active {
		return zero, failure("material_issue_closed")
	}
	if ticket.kinds[spec.Kind] {
		return zero, failure("material_issue_repeated")
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return zero, failure("material_issue_failed")
	}
	grant := MaterialGrant{Contract: MaterialContract, Token: hex.EncodeToString(nonce[:]), Invocation: s.invocation, ChunkBytes: MaterialFrameBytes, MaterialSpec: spec}
	pageBytes, err := materialGrantPageBytes(s.binding.Identity, grant, s.maxFrame)
	if err != nil {
		return zero, err
	}
	grant.ChunkBytes = pageBytes
	if err := s.check(ctx); err != nil {
		return zero, err
	}
	if err := guard(ctx); err != nil {
		return zero, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || ctx.Err() != nil || s.ctx.Err() != nil {
		return zero, failure("material_scope_closed")
	}
	if s.grants[grant.Token] != nil {
		return zero, failure("material_issue_failed")
	}
	s.grants[grant.Token] = &materialGrantState{grant: grant, port: port, guard: guard, digest: sha256.New()}
	ticket.kinds[spec.Kind] = true
	return grant, nil
}

// close linearizes revocation before waiting for in-flight grant IO. No user
// paths or persistent cache are enumerated; only these exact ports are closed.
func (s *materialHostScope) close() error {
	s.mu.Lock()
	if s.closed {
		done := s.closeDone
		s.mu.Unlock()
		<-done
		s.mu.Lock()
		err := s.closeErr
		s.mu.Unlock()
		return err
	}
	s.closed = true
	s.cancel()
	grants := make([]*materialGrantState, 0, len(s.grants))
	for _, g := range s.grants {
		grants = append(grants, g)
	}
	s.mu.Unlock()
	var first error
	for _, g := range grants {
		g.mu.Lock()
		if !g.closed {
			g.closed = true
			if err := g.port.Close(); first == nil {
				first = err
			}
		}
		g.mu.Unlock()
	}
	s.mu.Lock()
	s.closeErr = first
	close(s.closeDone)
	s.mu.Unlock()
	return first
}

func (s *materialHostScope) lookup(ctx context.Context, identity Identity, grant MaterialGrant) (*materialGrantState, error) {
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	if identity != s.binding.Identity || grant.Contract != MaterialContract || grant.Invocation != s.invocation {
		return nil, failure("material_identity_mismatch")
	}
	s.mu.Lock()
	g := s.grants[grant.Token]
	s.mu.Unlock()
	if g == nil || g.grant != grant {
		return nil, failure("material_grant_denied")
	}
	return g, nil
}

func (g *materialGrantState) check(ctx context.Context, s *materialHostScope) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if g.closed || g.failed || g.finished {
		return failure("material_grant_closed")
	}
	return g.guard(ctx)
}

func materialReadExact(ctx context.Context, read func(context.Context, int64, int) ([]byte, error), offset int64, n int) ([]byte, error) {
	data, err := read(ctx, offset, n)
	if err != nil {
		return nil, err
	}
	if len(data) != n {
		return nil, io.ErrUnexpectedEOF
	}
	return data, nil
}
