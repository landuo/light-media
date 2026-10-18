package larkhost

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	lark "light-media/internal/plugins/larkmessager"
	_ "modernc.org/sqlite"
)

// This isolated authority persists a separately encrypted anchor. It exercises
// the storage contract; it is not a fake installed App handler or run producer.
type ownedAuthority struct{ aead cipher.AEAD }

func (a ownedAuthority) encode(value Anchor) ([]byte, error) {
	raw, e := json.Marshal(value)
	if e != nil {
		return nil, e
	}
	nonce := make([]byte, a.aead.NonceSize())
	if _, e = io.ReadFull(rand.Reader, nonce); e != nil {
		return nil, e
	}
	return a.aead.Seal(nonce, nonce, raw, []byte("owned-independent/"+value.Namespace)), nil
}
func (a ownedAuthority) Validate(ctx context.Context, tx *sql.Tx, want Anchor) error {
	var sealed []byte
	if e := tx.QueryRowContext(ctx, `SELECT sealed FROM owned_authority WHERE namespace=?`, want.Namespace).Scan(&sealed); e != nil {
		return ErrIntegrity
	}
	n := a.aead.NonceSize()
	if len(sealed) < n+a.aead.Overhead() {
		return ErrIntegrity
	}
	raw, e := a.aead.Open(nil, sealed[:n], sealed[n:], []byte("owned-independent/"+want.Namespace))
	var got Anchor
	if e != nil || strict(raw, &got) != nil || got != want {
		return ErrIntegrity
	}
	return nil
}
func (a ownedAuthority) Advance(ctx context.Context, tx *sql.Tx, before, after Anchor) error {
	if before.Namespace != after.Namespace {
		return ErrIntegrity
	}
	if e := a.Validate(ctx, tx, before); e != nil {
		return e
	}
	sealed, e := a.encode(after)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `UPDATE owned_authority SET sealed=? WHERE namespace=?`, sealed, after.Namespace)
	return e
}

type journalFixture struct {
	t         *testing.T
	db        *sql.DB
	file      string
	key       []byte
	group     Group
	authority ownedAuthority
	store     *Store
	mu        sync.Mutex
	allowed   map[Scope]bool
	retired   atomic.Bool
}

func fixture(t *testing.T) *journalFixture {
	t.Helper()
	f := &journalFixture{t: t, file: filepath.Join(t.TempDir(), "owned.sqlite"), key: make([]byte, 32), group: Group{"LarkMessager.owned", 1}, allowed: map[Scope]bool{}}
	if _, e := rand.Read(f.key); e != nil {
		t.Fatal(e)
	}
	authorityKey := make([]byte, 32)
	if _, e := rand.Read(authorityKey); e != nil {
		t.Fatal(e)
	}
	b, _ := aes.NewCipher(authorityKey)
	f.authority.aead, _ = cipher.NewGCM(b)
	f.open()
	tx, e := f.db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(`CREATE TABLE owned_authority(namespace TEXT PRIMARY KEY,sealed BLOB NOT NULL)`); e != nil {
		t.Fatal(e)
	}
	a, e := GenesisTx(context.Background(), f.db, tx, f.key, f.group)
	if e != nil {
		t.Fatal(e)
	}
	sealed, e := f.authority.encode(a)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(`INSERT INTO owned_authority VALUES(?,?)`, a.Namespace, sealed); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	f.attach()
	t.Cleanup(func() {
		if f.db != nil {
			_ = f.db.Close()
		}
	})
	return f
}
func (f *journalFixture) open() {
	f.t.Helper()
	var e error
	f.db, e = sql.Open("sqlite", f.file)
	if e != nil {
		f.t.Fatal(e)
	}
	f.db.SetMaxOpenConns(1)
	if _, e = f.db.Exec(`PRAGMA busy_timeout=5000`); e != nil {
		f.t.Fatal(e)
	}
}
func (f *journalFixture) guard(_ context.Context, _ *sql.Tx, scope Scope) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.retired.Load() || !f.allowed[scope] {
		return lark.ErrDenied
	}
	return nil
}
func (f *journalFixture) attach() {
	f.t.Helper()
	var e error
	f.store, e = Attach(context.Background(), f.db, f.key, f.group, f.authority, f.guard)
	if e != nil {
		f.t.Fatal(e)
	}
}
func (f *journalFixture) cold() {
	f.t.Helper()
	if e := f.db.Close(); e != nil {
		f.t.Fatal(e)
	}
	f.open()
	f.attach()
}
func (f *journalFixture) scope(id string) Scope {
	s := Scope{Group: f.group, Revision: 1, InvocationID: id, SourceID: "owned-source/" + id, InputSHA: sha([]byte(id)), ConfigSHA: sha([]byte("owned-config")), Action: "module"}
	f.mu.Lock()
	f.allowed[s] = true
	f.mu.Unlock()
	return s
}
func (f *journalFixture) run(s Scope) *Run {
	f.t.Helper()
	r, e := f.store.ForRun(context.Background(), s)
	if e != nil {
		f.t.Fatal(e)
	}
	return r
}
func (f *journalFixture) adapter(scope Scope, origin string) *lark.Adapter {
	f.t.Helper()
	run := f.run(scope)
	b := lark.Binding{Owner: scope.Owner, Generation: scope.Generation, Revision: scope.Revision, InvocationID: scope.InvocationID, AppID: "OWNED_APP", AppSecret: "OWNED_APP_SECRET", Targets: map[string][]string{"open_id": {"ou_owned"}}}
	a, e := lark.NewAdapter(b, run, lark.Options{TestAPIOrigin: origin, Guard: func(ctx context.Context, got lark.Scope) error {
		if got.Owner != scope.Owner || got.Generation != scope.Generation || got.Revision != scope.Revision || got.InvocationID != scope.InvocationID {
			return lark.ErrDenied
		}
		return f.store.ValidateRun(ctx, scope)
	}})
	if e != nil {
		f.t.Fatal(e)
	}
	return a
}
func reply(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(r.URL.Path, "tenant_access_token") {
		io.WriteString(w, `{"code":0,"tenant_access_token":"OWNED_ACCESS_TOKEN","expire":7200}`)
		return
	}
	io.WriteString(w, `{"code":0,"data":{"message_id":"om_owned","chat_id":"oc_owned","msg_type":"text","private":"PRIVATE_OWNED_RESPONSE"}}`)
}

func TestConfirmedNativeSendReopensWithoutAnotherHTTPRequest(t *testing.T) {
	f := fixture(t)
	scope := f.scope("run-1")
	var calls, sends atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if strings.HasSuffix(r.URL.Path, "/messages") {
			sends.Add(1)
		}
		reply(w, r)
	}))
	defer server.Close()
	for i := 0; i < 2; i++ {
		if i != 0 {
			f.cold()
		}
		got, e := lark.NewClient(f.adapter(scope, server.URL)).SendText(context.Background(), "owned message", lark.Target{UserID: "ou_owned"})
		if e != nil || got == nil || !got.Success || got.MessageID != "om_owned" {
			t.Fatal("actual native client", e, got)
		}
	}
	if sends.Load() != 1 || calls.Load() != 2 {
		t.Fatal("confirmed receipt was sent again", sends.Load(), calls.Load())
	}
	raw, e := os.ReadFile(f.file)
	if e != nil {
		t.Fatal(e)
	}
	for _, secret := range []string{"PRIVATE_OWNED_RESPONSE", "OWNED_APP_SECRET", "OWNED_ACCESS_TOKEN"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatal("private value escaped encryption")
		}
	}
}

func TestLostRemoteReplyBlocksOnlyItsOriginalSourceAcrossReopen(t *testing.T) {
	f := fixture(t)
	scope := f.scope("uncertain")
	var sends atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/messages") && sends.Add(1) == 1 {
			c, _, e := w.(http.Hijacker).Hijack()
			if e != nil {
				t.Error(e)
				return
			}
			c.Close()
			return
		}
		reply(w, r)
	}))
	defer server.Close()
	_, e := lark.NewClient(f.adapter(scope, server.URL)).SendText(context.Background(), "owned", lark.Target{UserID: "ou_owned"})
	if !errors.Is(e, lark.ErrUnknown) || sends.Load() != 1 {
		t.Fatal(e, sends.Load())
	}
	f.cold()
	_, e = lark.NewClient(f.adapter(scope, server.URL)).SendText(context.Background(), "owned", lark.Target{UserID: "ou_owned"})
	if !errors.Is(e, lark.ErrUnknown) || sends.Load() != 1 {
		t.Fatal("unknown replay", e, sends.Load())
	}
	alias := f.scope("different-run")
	alias.SourceID = scope.SourceID
	f.mu.Lock()
	f.allowed[alias] = true
	f.mu.Unlock()
	if _, e = f.store.ForRun(context.Background(), alias); !errors.Is(e, ErrReplay) {
		t.Fatal("source fact got another timeline", e)
	}
	// A different real source notification is not frozen by an unrelated lost
	// response. Its own stable producer ID and guard are independently admitted.
	next := f.scope("independent-source")
	got, e := lark.NewClient(f.adapter(next, server.URL)).SendText(context.Background(), "a different owned message", lark.Target{UserID: "ou_owned"})
	if e != nil || got == nil || !got.Success || sends.Load() != 2 {
		t.Fatal("unrelated source was blocked", e, sends.Load())
	}
}

func TestLostLocalFinishReplyRecoversCommittedNativeResult(t *testing.T) {
	f := fixture(t)
	scope := f.scope("finish-loss")
	var sends atomic.Int32
	var lost atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/messages") {
			sends.Add(1)
		}
		reply(w, r)
	}))
	defer server.Close()
	f.store.afterCommit = func(op string) error {
		if op == "finish" && !lost.Swap(true) {
			return errors.New("owned lost commit reply")
		}
		return nil
	}
	_, e := lark.NewClient(f.adapter(scope, server.URL)).SendText(context.Background(), "owned", lark.Target{UserID: "ou_owned"})
	if !errors.Is(e, lark.ErrUnknown) || sends.Load() != 1 {
		t.Fatal(e, sends.Load())
	}
	f.cold()
	got, e := lark.NewClient(f.adapter(scope, server.URL)).SendText(context.Background(), "owned", lark.Target{UserID: "ou_owned"})
	if e != nil || got == nil || !got.Success || sends.Load() != 1 {
		t.Fatal("committed response did not recover", e, sends.Load())
	}
}

func TestLostBeginReplyDoesNotDispatchAndCannotBecomeFreshSend(t *testing.T) {
	f := fixture(t)
	scope := f.scope("begin-loss")
	var network atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { network.Add(1); reply(w, r) }))
	defer server.Close()
	f.store.afterCommit = func(op string) error {
		if op == "begin" {
			return errors.New("owned lost begin ack")
		}
		return nil
	}
	_, e := lark.NewClient(f.adapter(scope, server.URL)).SendText(context.Background(), "owned", lark.Target{UserID: "ou_owned"})
	if !errors.Is(e, lark.ErrUnknown) || network.Load() != 0 {
		t.Fatal(e, network.Load())
	}
	f.cold()
	_, e = lark.NewClient(f.adapter(scope, server.URL)).SendText(context.Background(), "owned", lark.Target{UserID: "ou_owned"})
	if !errors.Is(e, lark.ErrUnknown) || network.Load() != 0 {
		t.Fatal("unfinished admission repeated", e, network.Load())
	}
}

func TestRevokedDispatchCanStillRecordItsKnownRejection(t *testing.T) {
	f := fixture(t)
	scope := f.scope("revoked")
	var sends atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "tenant_access_token") {
			f.retired.Store(true)
		} else {
			sends.Add(1)
		}
		reply(w, r)
	}))
	defer server.Close()
	_, e := lark.NewClient(f.adapter(scope, server.URL)).SendText(context.Background(), "owned", lark.Target{UserID: "ou_owned"})
	if !errors.Is(e, lark.ErrDenied) || sends.Load() != 0 {
		t.Fatal(e, sends.Load())
	}
	tx, e := f.db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	_, _, cells, e := f.store.load(context.Background(), tx)
	if e != nil {
		t.Fatal(e)
	}
	if len(cells) != 1 {
		t.Fatal("lost known rejection", len(cells))
	}
	for _, c := range cells {
		if c.Receipt.State != "rejected" {
			t.Fatal(c.Receipt.State)
		}
	}
}

func TestScopeAndEvidenceCannotBeReboundOrReinitialized(t *testing.T) {
	for _, damage := range []string{"effect-delete", "effect-change", "root-delete", "anchor-delete", "all-journal-delete"} {
		t.Run(damage, func(t *testing.T) {
			f := fixture(t)
			scope := f.scope("owned")
			run := f.run(scope)
			intent := lark.Intent{ID: fmt.Sprintf("%s/1/1/owned/1", scope.Owner), Owner: scope.Owner, Generation: 1, Revision: 1, Digest: sha([]byte("request")), Op: "send"}
			if _, e := run.Begin(context.Background(), intent); e != nil {
				t.Fatal(e)
			}
			if e := run.Finish(context.Background(), lark.Receipt{Intent: intent, State: "unknown"}); e != nil {
				t.Fatal(e)
			}
			queries := map[string]string{"effect-delete": `DELETE FROM lark_native_effects`, "effect-change": `UPDATE lark_native_effects SET sealed=x'00'`, "root-delete": `DELETE FROM lark_native_roots`, "anchor-delete": `DELETE FROM owned_authority`, "all-journal-delete": `DELETE FROM lark_native_effects;DELETE FROM lark_native_roots`}
			if _, e := f.db.Exec(queries[damage]); e != nil {
				t.Fatal(e)
			}
			if _, e := Attach(context.Background(), f.db, f.key, f.group, f.authority, f.guard); e == nil {
				t.Fatal("damaged evidence admitted")
			}
			var count int
			if e := f.db.QueryRow(`SELECT COUNT(*) FROM lark_native_roots`).Scan(&count); e != nil {
				t.Fatal(e)
			}
			if (damage == "root-delete" || damage == "all-journal-delete") && count != 0 {
				t.Fatal("attachment created new genesis")
			}
		})
	}
	f := fixture(t)
	scope := f.scope("bound")
	run := f.run(scope)
	for _, change := range []string{"input", "config", "revision", "action"} {
		bad := scope
		switch change {
		case "input":
			bad.InputSHA = sha([]byte("other input"))
		case "config":
			bad.ConfigSHA = sha([]byte("other config"))
		case "revision":
			bad.Revision++
		case "action":
			bad.Action = "api"
		}
		f.mu.Lock()
		f.allowed[bad] = true
		f.mu.Unlock()
		if _, e := f.store.ForRun(context.Background(), bad); !errors.Is(e, ErrReplay) {
			t.Fatal("rebound immutable admission", change, e)
		}
	}
	bad := lark.Intent{ID: "other/1/1/bound/1", Owner: "other", Generation: 1, Revision: 1, Digest: sha([]byte("request")), Op: "send"}
	if _, e := run.Begin(context.Background(), bad); !errors.Is(e, lark.ErrDenied) {
		t.Fatal("another owner admitted", e)
	}
}

func TestConfirmedReplayRestoresNativeMessageGrantForNextEffect(t *testing.T) {
	f := fixture(t)
	scope := f.scope("derived")
	var sends, edits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PATCH" {
			edits.Add(1)
		} else if strings.HasSuffix(r.URL.Path, "/messages") {
			sends.Add(1)
		}
		reply(w, r)
	}))
	defer server.Close()
	_, e := lark.NewClient(f.adapter(scope, server.URL)).SendText(context.Background(), "owned", lark.Target{UserID: "ou_owned"})
	if e != nil {
		t.Fatal(e)
	}
	f.cold()
	adapter := f.adapter(scope, server.URL)
	_, e = lark.NewClient(adapter).SendText(context.Background(), "owned", lark.Target{UserID: "ou_owned"})
	if e != nil {
		t.Fatal(e)
	}
	_, e = adapter.API(context.Background(), lark.Request{Op: "edit", IDs: []string{"om_owned"}, Payload: lark.Object{"msg_type": "text", "content": lark.Object{"text": "edited"}}})
	if e != nil || sends.Load() != 1 || edits.Load() != 1 {
		t.Fatal("confirmed derived grant unavailable", e, sends.Load(), edits.Load())
	}
}

func TestParallelAdaptersDoNotSendSameSourceTwice(t *testing.T) {
	f := fixture(t)
	scope := f.scope("parallel")
	var sends atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/messages") {
			sends.Add(1)
		}
		reply(w, r)
	}))
	defer server.Close()
	adapters := []*lark.Adapter{f.adapter(scope, server.URL), f.adapter(scope, server.URL)}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, adapter := range adapters {
		wg.Add(1)
		go func(a *lark.Adapter) {
			defer wg.Done()
			_, e := lark.NewClient(a).SendText(context.Background(), "owned", lark.Target{UserID: "ou_owned"})
			results <- e
		}(adapter)
	}
	wg.Wait()
	close(results)
	for e := range results {
		if e != nil && !errors.Is(e, lark.ErrUnknown) {
			t.Fatal(e)
		}
	}
	if sends.Load() != 1 {
		t.Fatal("parallel source duplicated", sends.Load())
	}
}
