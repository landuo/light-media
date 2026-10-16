package seedhost

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"light-media/internal/download"
	_ "modernc.org/sqlite"
)

const badHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const goodHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

// This Core is a test-only SQLite adapter, never a production allow-all backend.
// It checks actual installed/run/downloader rows and frozen managed references.
type fixtureCore struct {
	db         *sql.DB
	mu         sync.Mutex
	binding    Binding
	refs       map[string]Downloader
	before     func(Effect)
	failCommit bool
	config     json.RawMessage
}
type ownerProof struct {
	Owner, Root, MediaKey string
	Snapshot              Stamp
}

func (c *fixtureCore) proof(q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, ctx context.Context, path string) (json.RawMessage, error) {
	var p ownerProof
	var b []byte
	err := q.QueryRowContext(ctx, `SELECT asset,root,media,snapshot FROM managed WHERE path=?`, path).Scan(&p.Owner, &p.Root, &p.MediaKey, &b)
	if err == sql.ErrNoRows {
		return json.RawMessage(`null`), nil
	}
	if err != nil {
		return nil, err
	}
	if json.Unmarshal(b, &p.Snapshot) != nil {
		return nil, ErrUnsafe
	}
	return raw(p), nil
}
func (c *fixtureCore) Pin(ctx context.Context, inv Invocation, p Pin) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.proof(c.db, ctx, p.Path)
}
func (c *fixtureCore) Guard(ctx context.Context, inv Invocation, effect Effect) (json.RawMessage, func(), error) {
	c.mu.Lock()
	fail := func(err error) (json.RawMessage, func(), error) { c.mu.Unlock(); return nil, nil, err }
	if c.before != nil {
		c.before(effect)
	}
	var g, r int64
	var enabled, installed bool
	var configSHA string
	if err := c.db.QueryRowContext(ctx, `SELECT generation,revision,installed,enabled,config_sha FROM core WHERE instance=?`, inv.Binding.InstanceID).Scan(&g, &r, &installed, &enabled, &configSHA); err != nil {
		return fail(err)
	}
	if !installed || !enabled || g != inv.Binding.Generation || effect.Binding != inv.Binding || effect.Authority.InstanceID != inv.Binding.InstanceID || effect.Authority.Generation != g || effect.Authority.Revision != r {
		return fail(ErrUnsafe)
	}
	var action, sha, state string
	var rg, rr int64
	if err := c.db.QueryRowContext(ctx, `SELECT generation,revision,action,input_sha,state FROM core_runs WHERE id=?`, inv.RunID).Scan(&rg, &rr, &action, &sha, &state); err != nil {
		return fail(err)
	}
	if rg != inv.Binding.Generation || rr != inv.Binding.Revision || action != inv.Action || sha != inv.InputSHA || state != "running" {
		return fail(ErrUnsafe)
	}
	if effect.Kind == "begin" && (inv.Action == "init" || inv.Action == "reload") && digest(effect.Args) != configSHA {
		return fail(ErrUnsafe)
	}
	if effect.Authority == inv.Binding {
		if effect.ConfigReceipt != "" {
			return fail(ErrUnsafe)
		}
	} else {
		var run string
		var origin, to int64
		var sha string
		if err := c.db.QueryRowContext(ctx, `SELECT run,origin_revision,to_revision,config_sha FROM core_config_receipts WHERE id=?`, effect.ConfigReceipt).Scan(&run, &origin, &to, &sha); err != nil {
			return fail(err)
		}
		var current string
		if err := c.db.QueryRowContext(ctx, `SELECT config_sha FROM core WHERE instance=?`, inv.Binding.InstanceID).Scan(&current); err != nil {
			return fail(err)
		}
		if run != inv.RunID || origin != inv.Binding.Revision || to != r || sha != current {
			return fail(ErrUnsafe)
		}
	}
	if effect.DownloaderID != "" {
		d, ok := c.refs[effect.DownloaderID]
		if !ok || d.Revision != effect.DownloaderRevision || d.ConfigSHA != effect.DownloaderSHA {
			return fail(ErrUnsafe)
		}
	}
	for path, frozen := range effect.ExpectedCore {
		now, err := c.proof(c.db, ctx, path)
		if err != nil || !bytes.Equal(now, frozen) {
			return fail(ErrUnsafe)
		}
	}
	return raw(effect.ExpectedCore), c.mu.Unlock, nil
}
func (c *fixtureCore) Commit(ctx context.Context, tx *sql.Tx, inv Invocation, effect Effect) error {
	if c.failCommit {
		return errors.New("synthetic Core receipt/index failure")
	}
	if effect.Kind == "file_step" {
		var proof map[string]json.RawMessage
		if json.Unmarshal(effect.Proof, &proof) != nil {
			return ErrUnsafe
		}
		for _, fact := range effect.Facts {
			expected, ok := proof[fact.Pin.Path]
			if !ok {
				return ErrUnsafe
			}
			current, err := c.proof(tx, ctx, fact.Pin.Path)
			if err != nil || !bytes.Equal(expected, current) {
				return ErrUnsafe
			}
			if string(expected) == "null" {
				continue
			}
			var p ownerProof
			if json.Unmarshal(expected, &p) != nil || p.Snapshot != fact.Pin.File {
				return ErrUnsafe
			}
			if _, err = os.Lstat(fact.Pin.Path); !errors.Is(err, os.ErrNotExist) {
				return ErrUnsafe
			}
			if _, err = tx.ExecContext(ctx, `UPDATE managed SET state='missing_pending' WHERE path=? AND asset=? AND root=? AND media=?`, fact.Pin.Path, p.Owner, p.Root, p.MediaKey); err != nil {
				return err
			}
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO core_receipts VALUES(?,?,?)`, effect.ID, inv.RunID, effect.Kind)
	return err
}
func (c *fixtureCore) Config(ctx context.Context, tx *sql.Tx, inv Invocation, effect Effect) (ConfigPublication, error) {
	next := effect.Authority
	next.Revision++
	result, err := tx.ExecContext(ctx, `UPDATE core SET revision=?,config_sha=? WHERE instance=? AND generation=? AND revision=?`, next.Revision, digest(effect.Args), next.InstanceID, next.Generation, effect.Authority.Revision)
	if err != nil {
		return ConfigPublication{Binding: next}, err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ConfigPublication{Binding: next}, ErrUnsafe
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO core_config_receipts VALUES(?,?,?,?,?,?,?,?)`, effect.ID, inv.RunID, inv.Binding.Revision, effect.Authority.Revision, next.Revision, effect.ConfigReceipt, digest(effect.Args), raw(effect))
	publication := ConfigPublication{Binding: next}
	if err != nil {
		return publication, err
	}
	for _, before := range effect.Queued {
		var state, phase, action, sha string
		var g, r int64
		if err = tx.QueryRowContext(ctx, `SELECT generation,revision,action,input_sha,state FROM core_runs WHERE id=?`, before.RunID).Scan(&g, &r, &action, &sha, &state); err != nil {
			return publication, err
		}
		if err = tx.QueryRowContext(ctx, `SELECT phase FROM core_deferred WHERE run=?`, before.RunID).Scan(&phase); err != nil {
			return publication, err
		}
		if g != before.Binding.Generation || r != before.Binding.Revision || action != before.Action || sha != before.InputSHA || state != "pending" || phase != "prepared" {
			return publication, ErrUnsafe
		}
		result, err = tx.ExecContext(ctx, `UPDATE core_runs SET revision=? WHERE id=? AND generation=? AND revision=? AND state='pending'`, next.Revision, before.RunID, g, r)
		if err != nil {
			return publication, err
		}
		n, _ = result.RowsAffected()
		if n != 1 {
			return publication, ErrUnsafe
		}
		after := before
		after.Binding = next
		publication.Rebound = append(publication.Rebound, after)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO core_trace(kind) VALUES('config')`)
	return publication, err
}
func (c *fixtureCore) Notice(ctx context.Context, tx *sql.Tx, inv Invocation, n Notice) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO core_notices VALUES(?,?,?)`, n.ID, inv.RunID, n.State)
	if err == nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO core_trace(kind) VALUES('notice')`)
	}
	return err
}

type fixture struct {
	db   *sql.DB
	j    *SQLJournal
	c    *fixtureCore
	o    *Owner
	root string
	key  []byte
	now  time.Time
	refs []Downloader
}

func newFixture(t *testing.T, refs []Downloader, roots []Root) *fixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err = db.Exec(`CREATE TABLE core(instance TEXT PRIMARY KEY,generation INTEGER,revision INTEGER,installed INTEGER,enabled INTEGER,config_sha TEXT);INSERT INTO core VALUES('CleanInvalidSeed',1,1,1,1,'initial');CREATE TABLE core_runs(id TEXT PRIMARY KEY,generation INTEGER,revision INTEGER,action TEXT,input_sha TEXT,state TEXT);CREATE TABLE core_deferred(run TEXT PRIMARY KEY,phase TEXT);CREATE TABLE core_receipts(id TEXT PRIMARY KEY,run TEXT,kind TEXT);CREATE TABLE core_notices(id TEXT PRIMARY KEY,run TEXT,state TEXT);CREATE TABLE core_trace(ordinal INTEGER PRIMARY KEY AUTOINCREMENT,kind TEXT);CREATE TABLE core_config_receipts(id TEXT PRIMARY KEY,run TEXT,origin_revision INTEGER,from_revision INTEGER,to_revision INTEGER,from_receipt TEXT,config_sha TEXT,effect BLOB);CREATE TABLE managed(path TEXT PRIMARY KEY,asset TEXT,root TEXT,media TEXT,snapshot BLOB,state TEXT);`); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{7}, 32)
	j, err := NewSQLJournal(db, key, "CleanInvalidSeed:1")
	if err != nil {
		t.Fatal(err)
	}
	c := &fixtureCore{db: db, binding: Binding{"CleanInvalidSeed", 1, 1}, refs: map[string]Downloader{}}
	for _, d := range refs {
		c.refs[d.ID] = d
	}
	f := &fixture{db: db, j: j, c: c, root: root, key: key, now: time.Date(2026, 10, 17, 1, 0, 0, 0, time.UTC), refs: refs}
	f.o, err = Open(Options{Binding: c.binding, Journal: j, Core: c, Downloaders: refs, Roots: roots, Now: func() time.Time { return f.now }, Timezone: "Asia/Shanghai"})
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *fixture) run(t *testing.T, id, action, event string) Invocation {
	t.Helper()
	var g, r int64
	if err := f.db.QueryRow(`SELECT generation,revision FROM core WHERE instance='CleanInvalidSeed'`).Scan(&g, &r); err != nil {
		t.Fatal(err)
	}
	inv := Invocation{Binding: Binding{"CleanInvalidSeed", g, r}, RunID: id, Action: action, EventID: event, InputSHA: digest([]any{id, action, event})}
	if _, err := f.db.Exec(`INSERT INTO core_runs VALUES(?,?,?,?,?,'running')`, id, g, r, action, inv.InputSHA); err != nil {
		t.Fatal(err)
	}
	return inv
}
func (f *fixture) config(t *testing.T, extra map[string]any) Invocation {
	t.Helper()
	names := []string{}
	for _, d := range f.refs {
		names = append(names, d.Name)
	}
	cfg := map[string]any{"enabled": true, "downloaders": names, "min_seeding_days": 0, "notify": false, "exclude_keywords": "", "exclude_labels": "", "exclude_categories": "", "onlyonce": false, "label_only": false, "delete_invalid_torrents": false, "detect_invalid_files": false, "delete_invalid_files": false}
	for k, v := range extra {
		cfg[k] = v
	}
	inv := f.run(t, "configure", "init", "")
	f.c.config = raw(cfg)
	if err := f.configure(t, inv, raw(cfg)); err != nil {
		t.Fatal(err)
	}
	return inv
}

type daemon struct {
	mu                      sync.Mutex
	kind                    string
	server                  *httptest.Server
	rows                    []map[string]any
	tags, deletes, trackers map[string]int
	failRead, failMutation  bool
	beforeMutation          func()
}

func newDaemon(t *testing.T, kind string) *daemon {
	t.Helper()
	d := &daemon{kind: kind, tags: map[string]int{}, deletes: map[string]int{}, trackers: map[string]int{}}
	d.server = httptest.NewServer(http.HandlerFunc(d.serve))
	t.Cleanup(d.server.Close)
	return d
}
func (d *daemon) serve(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	reply := func(v any) { w.Header().Set("Content-Type", "application/json"); _ = json.NewEncoder(w).Encode(v) }
	if d.kind == "qbittorrent" {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "synthetic-cookie", Path: "/"})
			io.WriteString(w, "Ok.")
		case "/api/v2/torrents/info":
			if d.failRead {
				http.Error(w, "synthetic passkey=do-not-log", 503)
				return
			}
			rows := []map[string]any{}
			for _, row := range d.rows {
				if filter := r.URL.Query().Get("hashes"); filter == "" || filter == row["hash"] {
					rows = append(rows, row)
				}
			}
			reply(rows)
		case "/api/v2/torrents/trackers":
			h := r.URL.Query().Get("hash")
			d.trackers[h]++
			status, msg := 2, ""
			if h == badHash {
				status, msg = 4, "torrent not registered with this tracker"
			}
			reply([]any{map[string]any{"url": "https://tracker.fixture/announce?passkey=synthetic-private", "status": status, "msg": msg, "tier": 0}})
		case "/api/v2/torrents/addTags", "/api/v2/torrents/delete":
			_ = r.ParseForm()
			h := r.Form.Get("hashes")
			if d.beforeMutation != nil {
				d.beforeMutation()
			}
			if strings.HasSuffix(r.URL.Path, "addTags") {
				d.tags[h]++
			} else {
				if r.Form.Get("deleteFiles") != "false" {
					http.Error(w, "data delete denied", 400)
					return
				}
				d.deletes[h]++
			}
			if d.failMutation {
				http.Error(w, "synthetic lost success body secret", 500)
				return
			}
		default:
			http.NotFound(w, r)
		}
		return
	}
	var q map[string]any
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	if decoder.Decode(&q) != nil {
		http.Error(w, "bad", 400)
		return
	}
	method, _ := q["method"].(string)
	args, _ := q["params"].(map[string]any)
	var result any = map[string]any{}
	switch method {
	case "session_get":
		result = map[string]any{"version": "4.1.0"}
	case "torrent_get":
		if d.failRead {
			http.Error(w, "unavailable", 503)
			return
		}
		rows := []map[string]any{}
		for _, row := range d.rows {
			keep := true
			if hashes, ok := args["ids"].([]any); ok {
				keep = false
				for _, h := range hashes {
					if h == row["hash_string"] {
						keep = true
					}
				}
			}
			if keep {
				rows = append(rows, row)
			}
		}
		result = map[string]any{"torrents": rows}
	case "torrent_set", "torrent_remove":
		ids, _ := args["ids"].([]any)
		if len(ids) != 1 {
			http.Error(w, "ids", 400)
			return
		}
		h, _ := ids[0].(string)
		if method == "torrent_set" {
			d.tags[h]++
		} else {
			if args["delete_local_data"] != false {
				http.Error(w, "data", 400)
				return
			}
			d.deletes[h]++
		}
		if d.failMutation {
			http.Error(w, "unknown", 500)
			return
		}
	default:
		http.Error(w, "unsupported", 400)
		return
	}
	reply(map[string]any{"jsonrpc": "2.0", "id": q["id"], "result": result})
}
func (d *daemon) bind(t *testing.T, id string) Downloader {
	t.Helper()
	ref, err := BindDownloader(id, 4, "frozen-private-config-digest", download.Config{ID: id, Kind: d.kind, BaseURL: d.server.URL, Username: "synthetic-user", Password: "synthetic-password"})
	if err != nil {
		t.Fatal(err)
	}
	return ref
}
func qbRow(hash, path string) map[string]any {
	return map[string]any{"hash": hash, "name": "owned", "content_path": path, "save_path": filepath.Dir(path), "category": "movies", "tags": "", "state": "uploading", "size": 4, "total_size": 4, "completed": 4, "progress": 1, "ratio": 1}
}
func trRow(hash, path string, id int) map[string]any {
	success, msg := true, "ok"
	if hash == badHash {
		success, msg = false, "torrent not registered with this tracker"
	}
	return map[string]any{"id": id, "name": filepath.Base(path), "download_dir": filepath.Dir(path), "hash_string": hash, "total_size": 4, "percent_done": 1, "status": 6, "labels": []string{"old"}, "error": 0, "error_string": "", "tracker_stats": []any{map[string]any{"announce": "https://tracker.fixture/announce?passkey=synthetic", "tier": 0, "last_announce_result": msg, "last_announce_succeeded": success}}, "trackers": []any{map[string]any{"announce": "https://tracker.fixture/announce", "tier": 0}}}
}

func TestPinnedNativeMutationsHaveReceiptsAndAlwaysKeepData(t *testing.T) {
	for _, kind := range []string{"qbittorrent", "transmission"} {
		for _, mode := range []string{"delete", "label"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				d := newDaemon(t, kind)
				local := filepath.Join(t.TempDir(), "owned-data")
				if err := os.WriteFile(local, []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
				if kind == "qbittorrent" {
					d.rows = []map[string]any{qbRow(badHash, local), qbRow(goodHash, local+"-good")}
				} else {
					d.rows = []map[string]any{trRow(badHash, local, 1), trRow(goodHash, local+"-good", 2)}
				}
				f := newFixture(t, []Downloader{d.bind(t, "pinned")}, nil)
				f.config(t, map[string]any{"delete_invalid_torrents": mode == "delete", "label_only": mode == "label", "label": "无效做种"})
				inv := f.run(t, "actual-clean", "run", "")
				if err := f.o.Execute(context.Background(), inv); err != nil {
					t.Fatal(err)
				}
				state, err := f.j.Read(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				writes := 0
				for _, effect := range state.Effects {
					if effect.Kind == "native_write" {
						writes++
						if effect.Phase != "confirmed" || effect.DownloaderID != "pinned" || effect.DownloaderRevision != 4 || len(effect.NativeSnapshot) == 0 || !bytes.Contains(effect.Result, []byte(`"observed":false`)) {
							t.Fatal("incorrect native receipt", effect)
						}
					}
				}
				if writes != 1 {
					t.Fatal("missing native effect", writes)
				}
				d.mu.Lock()
				tagged, deleted := d.tags[badHash], d.deletes[badHash]
				badReads := d.trackers[badHash]
				d.mu.Unlock()
				if (mode == "label" && tagged != 1) || (mode == "delete" && deleted != 1) {
					t.Fatal("wrong actual protocol action", tagged, deleted)
				}
				if kind == "qbittorrent" && badReads != 6 {
					t.Fatal("source lazy tracker reads lost", badReads)
				}
				if data, err := os.ReadFile(local); err != nil || string(data) != "keep" {
					t.Fatal("torrent cleanup deleted downloaded data", err)
				}
			})
		}
	}
}
func TestUnknownRemoteOutcomeSurvivesReopenAndCannotReplayOrEraseAcceptedNotices(t *testing.T) {
	d := newDaemon(t, "qbittorrent")
	d.rows = []map[string]any{qbRow(badHash, "/daemon/a"), qbRow(goodHash, "/daemon/b")}
	d.failMutation = true
	f := newFixture(t, []Downloader{d.bind(t, "q")}, nil)
	f.config(t, map[string]any{"delete_invalid_torrents": true, "notify": true})
	inv := f.run(t, "uncertain-write", "run", "")
	if err := f.o.Execute(context.Background(), inv); err == nil {
		t.Fatal("swallowed wrapper false became host success")
	}
	s, err := f.j.Read(context.Background())
	if err != nil || s.Runs[inv.RunID].Phase != "unknown" {
		t.Fatal("unknown not persisted", err)
	}
	var pending int
	if err = f.db.QueryRow(`SELECT count(*) FROM core_notices WHERE state='pending'`).Scan(&pending); err != nil || pending != 2 {
		t.Fatal("source notices accepted before finish were erased", pending, err)
	}
	for _, n := range s.Notices {
		if n.State != "pending" {
			t.Fatal("local notice acceptance was conflated with external delivery")
		}
	}
	var sealed []byte
	if err = f.db.QueryRow(`SELECT sealed FROM seed_host_state`).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"passkey", "synthetic-password", badHash, "torrent not registered"} {
		if bytes.Contains(sealed, []byte(private)) {
			t.Fatal("private state plaintext", private)
		}
	}
	reopened, err := Open(f.o.opts)
	if err != nil {
		t.Fatal(err)
	}
	if err = reopened.Execute(context.Background(), inv); !errors.Is(err, ErrUnknown) {
		t.Fatal("old invocation replayed", err)
	}
	next := f.run(t, "another-run", "run", "")
	if err = reopened.Execute(context.Background(), next); !errors.Is(err, ErrUnknown) {
		t.Fatal("unresolved mutation allowed new execution", err)
	}
	d.mu.Lock()
	count := d.deletes[badHash]
	d.mu.Unlock()
	if count != 1 {
		t.Fatal("remote mutation duplicated", count)
	}
}
func makeSource(t *testing.T) (string, Root) {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, err := AuthorizeRoot("source-root", path, "source")
	if err != nil {
		t.Fatal(err)
	}
	return path, root
}
func TestOwnedFilesAndTreeUseSnapshotReceiptsWithoutDeletingSeededData(t *testing.T) {
	source, root := makeSource(t)
	orphan := filepath.Join(source, "orphan.mkv")
	seeded := filepath.Join(source, "seeded.mkv")
	tree := filepath.Join(source, "old-tree")
	if err := os.MkdirAll(filepath.Join(tree, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{orphan, seeded, filepath.Join(tree, "nested", "a.mkv"), filepath.Join(tree, "readme.nfo")} {
		if err := os.WriteFile(path, []byte("owned fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	mediaDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(mediaDir, "kept-hardlink.mkv")
	if err = os.Link(orphan, media); err != nil {
		t.Fatal(err)
	}
	d := newDaemon(t, "qbittorrent")
	d.rows = []map[string]any{qbRow(goodHash, seeded)}
	f := newFixture(t, []Downloader{d.bind(t, "q")}, []Root{root})
	f.config(t, map[string]any{"detect_invalid_files": true, "delete_invalid_files": true, "download_dirs": source + ":" + source})
	inv := f.run(t, "file-clean", "run", "")
	if err = f.o.Execute(context.Background(), inv); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{orphan, tree} {
		if _, err = os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("orphan retained", path, err)
		}
	}
	for _, path := range []string{seeded, media} {
		if _, err = os.Stat(path); err != nil {
			t.Fatal("seeded file or media hardlink deleted", path, err)
		}
	}
	s, _ := f.j.Read(context.Background())
	steps := 0
	for _, effect := range s.Effects {
		if effect.Kind == "file_step" {
			steps++
			if effect.Phase != "confirmed" {
				t.Fatal("file effect not confirmed")
			}
		}
	}
	if steps < 5 {
		t.Fatal("tree leaves lacked individual durable facts", steps)
	}
}
func TestFailedNativeListCannotAuthorizeFileDeletion(t *testing.T) {
	source, root := makeSource(t)
	path := filepath.Join(source, "owned.mkv")
	if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	d := newDaemon(t, "qbittorrent")
	d.failRead = true
	f := newFixture(t, []Downloader{d.bind(t, "q")}, []Root{root})
	f.config(t, map[string]any{"detect_invalid_files": true, "delete_invalid_files": true, "download_dirs": source + ":" + source})
	inv := f.run(t, "read-failed", "run", "")
	if err := f.o.Execute(context.Background(), inv); err == nil {
		t.Fatal("partial native evidence falsely succeeded")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("native read failure deleted source", err)
	}
	s, _ := f.j.Read(context.Background())
	if len(s.Effects) != 0 {
		t.Fatal("file mutation intent granted despite incomplete content list")
	}
}
func TestFileSnapshotAndFrozenManagedOwnerRejectReplacement(t *testing.T) {
	for _, mode := range []string{"inode", "managed-owner", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			source, root := makeSource(t)
			path := filepath.Join(source, "owned.mkv")
			if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			d := newDaemon(t, "qbittorrent")
			d.rows = []map[string]any{}
			f := newFixture(t, []Downloader{d.bind(t, "q")}, []Root{root})
			f.config(t, map[string]any{"detect_invalid_files": true, "delete_invalid_files": true, "download_dirs": source + ":" + source})
			st, _ := os.Stat(path)
			snapshot, _ := fileStamp(st)
			if _, err := f.db.Exec(`INSERT INTO managed VALUES(?,?,?,?,?,'present')`, path, "old-asset", root.ID, "movie:1", raw(snapshot)); err != nil {
				t.Fatal(err)
			}
			changed := false
			f.c.before = func(effect Effect) {
				if changed || effect.Kind != "file_delete" {
					return
				}
				changed = true
				if mode == "managed-owner" {
					_, _ = f.db.Exec(`UPDATE managed SET asset='new-asset' WHERE path=?`, path)
					return
				}
				if err := os.Rename(path, path+".old"); err != nil {
					t.Fatal(err)
				}
				if mode == "symlink" {
					if err := os.Symlink(path+".old", path); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(path, []byte("new-owner"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			inv := f.run(t, "replace-safe", "run", "")
			if err := f.o.Execute(context.Background(), inv); err == nil {
				t.Fatal("replacement granted deletion")
			}
			if _, err := os.Lstat(path); err != nil {
				t.Fatal("later same-path resource deleted", err)
			}
			var state string
			if err := f.db.QueryRow(`SELECT state FROM managed WHERE path=?`, path).Scan(&state); err != nil || state != "present" {
				t.Fatal("new owner marked missing", state, err)
			}
		})
	}
}

func TestOnlyOnceRewriteAndThreeSecondPlanPublishAtomicallyAndDoNotReplay(t *testing.T) {
	d := newDaemon(t, "qbittorrent")
	d.rows = []map[string]any{}
	f := newFixture(t, []Downloader{d.bind(t, "q")}, nil)
	configured := f.config(t, map[string]any{"enabled": false, "onlyonce": true})
	state, err := f.j.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	_ = json.Unmarshal(state.Config, &config)
	trigger, ok := state.Triggers["once:"+configured.RunID]
	if config["onlyonce"] != false || !ok || trigger.Binding.Revision != 2 || !trigger.Due.Equal(f.now.Add(3*time.Second)) || len(state.ConfigHistory) != 1 {
		t.Fatal("date/flag/config scope not atomic", state.Binding, trigger)
	}
	calls := 0
	enqueue := func(tx *sql.Tx, tr Trigger) (Invocation, error) {
		calls++
		inv := Invocation{Binding: tr.Binding, RunID: "once-run", Action: "once", EventID: tr.ID, InputSHA: digest([]any{tr.ID, "once"})}
		_, err := tx.Exec(`INSERT INTO core_runs VALUES(?,?,?,?,?,'running')`, inv.RunID, inv.Binding.Generation, inv.Binding.Revision, inv.Action, inv.InputSHA)
		return inv, err
	}
	if err = f.o.ClaimDue(context.Background(), f.now.Add(2*time.Second), enqueue); err != nil || calls != 0 {
		t.Fatal("date ran before three seconds", calls, err)
	}
	if err = f.o.ClaimDue(context.Background(), f.now.Add(3*time.Second), enqueue); err != nil || calls != 1 {
		t.Fatal(err)
	}
	state, _ = f.j.Read(context.Background())
	queued := state.Triggers[trigger.ID]
	inv := Invocation{Binding: queued.Binding, RunID: queued.QueuedRunID, Action: "once", EventID: queued.ID, InputSHA: digest([]any{queued.ID, "once"})}
	f.now = queued.Due
	if err = f.o.Execute(context.Background(), inv); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(f.o.opts)
	if err != nil {
		t.Fatal(err)
	}
	if err = reopened.ClaimDue(context.Background(), f.now.Add(time.Hour), enqueue); err != nil || calls != 1 {
		t.Fatal("old onlyonce re-enqueued", calls, err)
	}
	if _, found, err := reopened.Outcome(context.Background(), configured); err != nil || !found {
		t.Fatal("config receipt lost", found, err)
	}
}
func TestCronAndDueEnqueueRollbackStopAndCommandBinding(t *testing.T) {
	d := newDaemon(t, "qbittorrent")
	d.rows = []map[string]any{}
	f := newFixture(t, []Downloader{d.bind(t, "q")}, nil)
	f.config(t, map[string]any{"cron": "*/5 * * * *"})
	s, _ := f.j.Read(context.Background())
	var service Trigger
	for _, tr := range s.Triggers {
		if tr.Kind == "service" {
			service = tr
		}
	}
	if service.Spec == nil || service.Spec.Timezone != "Asia/Shanghai" {
		t.Fatal("missing original timezone cron")
	}
	err := f.o.ClaimDue(context.Background(), service.Due, func(tx *sql.Tx, tr Trigger) (Invocation, error) {
		_, err := tx.Exec(`INSERT INTO core_runs VALUES('rolled-back',1,1,'service','test','running')`)
		if err != nil {
			return Invocation{}, err
		}
		return Invocation{}, errors.New("synthetic enqueue CAS rejection")
	})
	if err == nil {
		t.Fatal("enqueue failure ignored")
	}
	var count int
	_ = f.db.QueryRow(`SELECT count(*) FROM core_runs WHERE id='rolled-back'`).Scan(&count)
	if count != 0 {
		t.Fatal("run committed without due marker")
	}
	s, _ = f.j.Read(context.Background())
	if s.Triggers[service.ID].Phase != "ready" {
		t.Fatal("failed enqueue consumed cron")
	}
	tr := Trigger{ID: "trusted-command", Kind: "command", Binding: s.Binding, Command: map[string]any{"action": "toggle_notify_all", "channel": "synthetic-channel", "user": "synthetic-user"}}
	var command Invocation
	if err = f.o.PublishCommand(context.Background(), tr, func(tx *sql.Tx, tr Trigger) (Invocation, error) {
		command = Invocation{Binding: tr.Binding, RunID: "command-run", Action: "command", EventID: tr.ID, InputSHA: digest(tr.Command)}
		_, err := tx.Exec(`INSERT INTO core_runs VALUES(?,?,?,?,?,'running')`, command.RunID, 1, tr.Binding.Revision, command.Action, command.InputSHA)
		return command, err
	}); err != nil {
		t.Fatal(err)
	}
	forged := command
	forged.EventID = "unbound"
	if err = f.o.Execute(context.Background(), forged); err == nil {
		t.Fatal("worker changed command event identity")
	}
	// The forged attempt consumes that RunID conservatively; a valid command
	// needs its actual immutable invocation. This second fixture proves success.
	f2 := newFixture(t, []Downloader{d.bind(t, "q")}, nil)
	f2.config(t, nil)
	s, _ = f2.j.Read(context.Background())
	tr.Binding = s.Binding
	if err = f2.o.PublishCommand(context.Background(), tr, func(tx *sql.Tx, tr Trigger) (Invocation, error) {
		command = Invocation{Binding: tr.Binding, RunID: "valid-command", Action: "command", EventID: tr.ID, InputSHA: digest(tr.Command)}
		_, err := tx.Exec(`INSERT INTO core_runs VALUES(?,?,?,?,?,'running')`, command.RunID, 1, tr.Binding.Revision, command.Action, command.InputSHA)
		return command, err
	}); err != nil {
		t.Fatal(err)
	}
	if err = f2.o.Execute(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	s, _ = f2.j.Read(context.Background())
	var cfg map[string]any
	_ = json.Unmarshal(s.Config, &cfg)
	if cfg["notify_all"] != true || len(s.ConfigHistory) != 1 || len(s.Notices) != 2 {
		t.Fatal("original toggle command/history/notices lost")
	}
	if err = f.o.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	s, _ = f.j.Read(context.Background())
	if s.Triggers[service.ID].Phase != "canceled" {
		t.Fatal("stop retained future cron")
	}
}
func TestConfigSQLFailureRollsBackFlagCoreRevisionAndSchedule(t *testing.T) {
	d := newDaemon(t, "qbittorrent")
	f := newFixture(t, []Downloader{d.bind(t, "q")}, nil)
	if _, err := f.db.Exec(`CREATE TRIGGER fail_config_receipt BEFORE INSERT ON core_config_receipts BEGIN SELECT RAISE(ABORT,'synthetic config receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	// Core.Config updates the core revision first; receipt insertion then fails
	// inside the same actual transaction as the date plan and source flag.
	inv := f.run(t, "onlyonce-config-failed", "init", "")
	err := f.configure(t, inv, raw(map[string]any{"onlyonce": true, "enabled": false, "downloaders": []string{"q"}}))
	if err == nil {
		t.Fatal("SQL failure ignored")
	}
	var revision int64
	if err = f.db.QueryRow(`SELECT revision FROM core`).Scan(&revision); err != nil || revision != 1 {
		t.Fatal("clear flag revision escaped rollback", revision, err)
	}
	s, _ := f.j.Read(context.Background())
	if len(s.Triggers) != 0 || len(s.ConfigHistory) != 0 || len(s.Config) != 0 {
		t.Fatal("schedule/flag committed separately from core")
	}
}
func TestConfirmedFileFactRepairsCoreAfterSQLFailureWithoutDeletingReplacement(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		t.Run(map[bool]string{false: "same_owner", true: "new_owner"}[replacement], func(t *testing.T) {
			source, root := makeSource(t)
			path := filepath.Join(source, "managed.mkv")
			if err := os.WriteFile(path, []byte("owned"), 0600); err != nil {
				t.Fatal(err)
			}
			info, _ := os.Stat(path)
			stamp, _ := fileStamp(info)
			d := newDaemon(t, "qbittorrent")
			d.rows = []map[string]any{}
			f := newFixture(t, []Downloader{d.bind(t, "q")}, []Root{root})
			f.config(t, map[string]any{"detect_invalid_files": true, "delete_invalid_files": true, "download_dirs": source + ":" + source})
			if _, err := f.db.Exec(`INSERT INTO managed VALUES(?,?,?,?,?,'present')`, path, "asset-1", root.ID, "movie:1", raw(stamp)); err != nil {
				t.Fatal(err)
			}
			f.c.failCommit = true
			inv := f.run(t, "failed-core-index", "run", "")
			if err := f.o.Execute(context.Background(), inv); err == nil {
				t.Fatal("Core failure ignored")
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("fixture did not actually unlink file", err)
			}
			s, _ := f.j.Read(context.Background())
			facts := 0
			for _, e := range s.Effects {
				if e.Kind == "file_step" && e.Phase == "fs_confirmed" {
					facts++
				}
			}
			if facts != 1 {
				t.Fatal("actual FS fact lost on Core failure", facts)
			}
			f.c.failCommit = false
			if replacement {
				if err := os.WriteFile(path, []byte("new file"), 0600); err != nil {
					t.Fatal(err)
				}
				_, _ = f.db.Exec(`UPDATE managed SET asset='new-asset' WHERE path=?`, path)
			}
			err := f.o.ReconcileConfirmedFiles(context.Background(), inv)
			if replacement {
				if err == nil {
					t.Fatal("old fact rebound to new owner")
				}
				if bytes, readErr := os.ReadFile(path); readErr != nil || string(bytes) != "new file" {
					t.Fatal("reconcile removed replacement", readErr)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				var state string
				_ = f.db.QueryRow(`SELECT state FROM managed WHERE path=?`, path).Scan(&state)
				if state != "missing_pending" {
					t.Fatal("known missing file index not repaired", state)
				}
			}
		})
	}
}

func publishFixtureCommand(t *testing.T, f *fixture, id, action string) Invocation {
	t.Helper()
	s, err := f.j.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tr := Trigger{ID: id, Kind: "command", Binding: s.Binding, Command: map[string]any{"action": action, "channel": "fixture", "user": "fixture"}}
	var inv Invocation
	err = f.o.PublishCommand(context.Background(), tr, func(tx *sql.Tx, tr Trigger) (Invocation, error) {
		inv = Invocation{Binding: tr.Binding, RunID: id + ":run", Action: "command", EventID: tr.ID, InputSHA: digest(tr.Command)}
		_, err := tx.Exec(`INSERT INTO core_runs VALUES(?,?,?,?,?,'running')`, inv.RunID, inv.Binding.Generation, inv.Binding.Revision, inv.Action, inv.InputSHA)
		return inv, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return inv
}
func TestSourceConfigAndNoticePrefixesSurviveLaterFailures(t *testing.T) {
	t.Run("toggle_config_then_second_notice_failure", func(t *testing.T) {
		d := newDaemon(t, "qbittorrent")
		d.rows = []map[string]any{}
		f := newFixture(t, []Downloader{d.bind(t, "q")}, nil)
		f.config(t, nil)
		if _, err := f.db.Exec(`CREATE TRIGGER fail_second_notice BEFORE INSERT ON core_notices WHEN NEW.id LIKE '%:notice:3' BEGIN SELECT RAISE(ABORT,'synthetic second notice failure'); END`); err != nil {
			t.Fatal(err)
		}
		inv := publishFixtureCommand(t, f, "toggle-prefix", "toggle_notify_all")
		if err := f.o.Execute(context.Background(), inv); err == nil {
			t.Fatal("notice failure ignored")
		}
		s, err := f.j.Read(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		var cfg map[string]any
		_ = json.Unmarshal(s.Config, &cfg)
		var revision int64
		_ = f.db.QueryRow(`SELECT revision FROM core`).Scan(&revision)
		r := s.Runs[inv.RunID]
		if cfg["notify_all"] != true || revision != 2 || len(s.ConfigHistory) != 1 || len(s.Notices) != 1 || r.Invocation != inv || r.Effective.Revision != 2 || r.ConfigReceipt == "" || r.Phase != "failed" {
			t.Fatal("source prefix not durable", revision, len(s.Notices), r)
		}
		assertSourceOraclePrefix(t, f, "toggle_second_notice_failure")
		for _, n := range s.Notices {
			if n.State != "pending" || !bytes.Contains(n.Payload, []byte("开始执行")) {
				t.Fatal("start prefix rewritten after failure")
			}
		}
		for _, effect := range s.Effects {
			if effect.Kind == "config_write" && (effect.Binding != inv.Binding || effect.Authority != inv.Binding) {
				t.Fatal("config rewrite changed original invocation")
			}
		}
	})
	t.Run("start_notice_then_downloader_list_failure", func(t *testing.T) {
		d := newDaemon(t, "qbittorrent")
		d.failRead = true
		f := newFixture(t, []Downloader{d.bind(t, "q")}, nil)
		f.config(t, nil)
		inv := publishFixtureCommand(t, f, "failed-list-prefix", "detect_invalid_torrents")
		if err := f.o.Execute(context.Background(), inv); err == nil {
			t.Fatal("read failure swallowed by source became host success")
		}
		s, err := f.j.Read(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		first := s.Notices[inv.RunID+":notice:1"]
		if first.State != "pending" || !bytes.Contains(first.Payload, []byte("开始执行")) {
			t.Fatal("accepted start notice lost")
		}
		if s.Runs[inv.RunID].Phase != "failed" {
			t.Fatal("read denial falsely classified as uncertain mutation", s.Runs[inv.RunID])
		}
	})
	t.Run("onlyonce_flag_then_invalid_service_cron", func(t *testing.T) {
		f := newFixture(t, nil, nil)
		inv := f.run(t, "once-invalid-cron", "init", "")
		err := f.configure(t, inv, raw(map[string]any{"enabled": true, "onlyonce": true, "cron": "bad cron", "min_seeding_days": 0}))
		if err == nil {
			t.Fatal("service cron error hidden")
		}
		s, _ := f.j.Read(context.Background())
		var cfg map[string]any
		_ = json.Unmarshal(s.Config, &cfg)
		tr, ok := s.Triggers["once:"+inv.RunID]
		if !ok || tr.Phase != "ready" || tr.Binding.Revision != 2 || !tr.Due.Equal(f.now.Add(3*time.Second)) || cfg["onlyonce"] != false || len(s.ConfigHistory) != 1 {
			t.Fatal("later cron error erased flag/date prefix")
		}
		for _, tr := range s.Triggers {
			if tr.Kind == "service" {
				t.Fatal("invalid cron installed")
			}
		}
	})
}
func TestStartedRunWithoutEffectsBlocksNewInvocationAfterActualDatabaseReopen(t *testing.T) {
	f := newFixture(t, nil, nil)
	inv := f.run(t, "interrupted-before-source", "init", "")
	// Simulate process loss immediately after durable begin, before any source call.
	if _, err := f.db.Exec(`UPDATE core SET config_sha=?`, digest(raw(map[string]any{"enabled": false}))); err != nil {
		t.Fatal(err)
	}
	e, err := f.o.enter(context.Background(), inv, raw(map[string]any{"enabled": false}))
	if err != nil {
		t.Fatal(err)
	}
	e.cancel()
	<-f.o.gate
	if err = f.db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(f.root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	j, err := NewSQLJournal(db, f.key, "CleanInvalidSeed:1")
	if err != nil {
		t.Fatal(err)
	}
	f.db, f.j, f.c.db = db, j, db
	opts := f.o.opts
	opts.Journal = j
	reopened, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	next := f.run(t, "do-not-auto-replay", "init", "")
	if err = reopened.Configure(context.Background(), next, raw(map[string]any{"enabled": false})); !errors.Is(err, ErrUnknown) {
		t.Fatal("unknown run without individual effects replayed", err)
	}
	if _, found, err := reopened.Outcome(context.Background(), inv); err != nil || found {
		t.Fatal("interrupted run forged completion", found, err)
	}
}
func TestNativeGuardDenialCannotBeHiddenBySourceWrapper(t *testing.T) {
	d := newDaemon(t, "qbittorrent")
	d.rows = []map[string]any{qbRow(badHash, "/daemon/a"), qbRow(goodHash, "/daemon/b")}
	f := newFixture(t, []Downloader{d.bind(t, "q")}, nil)
	f.config(t, map[string]any{"delete_invalid_torrents": true})
	f.c.before = func(e Effect) {
		if e.Kind == "native_write" {
			delete(f.c.refs, e.DownloaderID)
		}
	}
	inv := f.run(t, "deny-write", "run", "")
	if err := f.o.Execute(context.Background(), inv); err == nil {
		t.Fatal("wrapper ignored rejected native mutation")
	}
	d.mu.Lock()
	deletes := d.deletes[badHash]
	d.mu.Unlock()
	if deletes != 0 {
		t.Fatal("guard denied but daemon mutated")
	}
	s, _ := f.j.Read(context.Background())
	if s.Runs[inv.RunID].Phase != "failed" {
		t.Fatal("denied mutation marked completed/unknown")
	}
	for _, e := range s.Effects {
		if e.Kind == "native_write" {
			t.Fatal("denied write got an intent")
		}
	}
}
func TestOwnerGateCancellationAndImmutableDownloaderReference(t *testing.T) {
	f := newFixture(t, nil, nil)
	f.o.gate <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	inv := f.run(t, "gate-wait", "init", "")
	if err := f.o.Configure(ctx, inv, raw(map[string]any{})); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("gate wait ignored cancellation", err)
	}
	<-f.o.gate
	s, err := f.j.Read(context.Background())
	if err != nil || len(s.Runs) != 0 {
		t.Fatal("cancelled gate wrote a run", err)
	}
	d := newDaemon(t, "qbittorrent")
	ref := d.bind(t, "actual-config")
	ref.ID = "forged-config"
	opts := f.o.opts
	opts.Downloaders = []Downloader{ref}
	if _, err := Open(opts); !errors.Is(err, ErrUnsafe) {
		t.Fatal("mutated configured downloader ref accepted", err)
	}
}

func assertSourceOraclePrefix(t *testing.T, f *fixture, name string) {
	t.Helper()
	b, err := os.ReadFile("../../../../testdata/plugins/clean-invalid-seed-host/reference/source-prefix.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Source string `json:"source_sha256"`
		Cases  []struct {
			Case   string   `json:"case"`
			Prefix []string `json:"committed_prefix"`
		} `json:"cases"`
	}
	if json.Unmarshal(b, &oracle) != nil || oracle.Source != "fd398230ee132f6002dbaf892e28fb10b9b95567fda9ce513e1543329a8b7fda" {
		t.Fatal("invalid source oracle")
	}
	var want []string
	found := false
	for _, c := range oracle.Cases {
		if c.Case == name {
			want = c.Prefix
			found = true
		}
	}
	if !found {
		t.Fatal("missing source case", name)
	}
	rows, err := f.db.Query(`SELECT kind FROM core_trace ORDER BY ordinal`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := []string{}
	for rows.Next() {
		var kind string
		if err = rows.Scan(&kind); err != nil {
			t.Fatal(err)
		}
		got = append(got, kind)
	}
	if err = rows.Err(); err != nil || digest(got) != digest(want) {
		t.Fatal("actual Core SQL prefix differs from installed AST", got, want, err)
	}
}
func TestCommandSourceFailurePreservesInstalledASTNoticePrefix(t *testing.T) {
	d := newDaemon(t, "qbittorrent")
	d.rows = []map[string]any{}
	f := newFixture(t, []Downloader{d.bind(t, "q")}, nil)
	// This creates an actual Go source error inside Clean after the accepted
	// command notice. The AST oracle separately injects at its Clean call boundary.
	f.config(t, map[string]any{"exclude_categories": 123})
	inv := publishFixtureCommand(t, f, "source-prefix", "detect_invalid_torrents")
	if err := f.o.Execute(context.Background(), inv); err == nil {
		t.Fatal("source error swallowed")
	}
	assertSourceOraclePrefix(t, f, "command_clean_stub_failure")
}
func TestInternalConfigRevisionRebindsOnlyExactUnstartedQueuedInvocationAtomically(t *testing.T) {
	for _, mode := range []string{"pending_prepared", "started", "late_sql_failure"} {
		t.Run(mode, func(t *testing.T) {
			d := newDaemon(t, "qbittorrent")
			d.rows = []map[string]any{}
			f := newFixture(t, []Downloader{d.bind(t, "q")}, nil)
			f.config(t, map[string]any{"cron": "*/5 * * * *"})
			state, _ := f.j.Read(context.Background())
			var service Trigger
			for _, tr := range state.Triggers {
				if tr.Kind == "service" {
					service = tr
				}
			}
			queued := Invocation{}
			if err := f.o.ClaimDue(context.Background(), service.Due, func(tx *sql.Tx, tr Trigger) (Invocation, error) {
				queued = Invocation{Binding: tr.Binding, RunID: "queued-service", Action: "service", EventID: tr.ID, InputSHA: digest(tr.ID)}
				if _, err := tx.Exec(`INSERT INTO core_runs VALUES(?,?,?,?,?,'pending')`, queued.RunID, queued.Binding.Generation, queued.Binding.Revision, queued.Action, queued.InputSHA); err != nil {
					return queued, err
				}
				_, err := tx.Exec(`INSERT INTO core_deferred VALUES(?,'prepared')`, queued.RunID)
				return queued, err
			}); err != nil {
				t.Fatal(err)
			}
			if mode == "started" {
				_, _ = f.db.Exec(`UPDATE core_runs SET state='running' WHERE id=?`, queued.RunID)
				_, _ = f.db.Exec(`UPDATE core_deferred SET phase='started' WHERE run=?`, queued.RunID)
			}
			if mode == "late_sql_failure" {
				if _, err := f.db.Exec(`CREATE TRIGGER reject_rebind BEFORE UPDATE OF revision ON core_runs WHEN OLD.id='queued-service' BEGIN SELECT RAISE(ABORT,'synthetic late rebind failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			command := publishFixtureCommand(t, f, "toggle-with-queued", "toggle_notify_all")
			err := f.o.Execute(context.Background(), command)
			state, _ = f.j.Read(context.Background())
			var actualRevision, queuedRevision, originRevision int64
			_ = f.db.QueryRow(`SELECT revision FROM core`).Scan(&actualRevision)
			_ = f.db.QueryRow(`SELECT revision FROM core_runs WHERE id=?`, queued.RunID).Scan(&queuedRevision)
			_ = f.db.QueryRow(`SELECT revision FROM core_runs WHERE id=?`, command.RunID).Scan(&originRevision)
			if originRevision != 1 {
				t.Fatal("active original invocation was rewritten")
			}
			if mode != "pending_prepared" {
				if err == nil || actualRevision != 1 || queuedRevision != 1 || state.Triggers[service.ID].Binding.Revision != 1 || len(state.ConfigHistory) != 0 || len(state.Notices) != 1 {
					t.Fatal("unsafe/failed queued CAS partially committed", err, actualRevision, queuedRevision)
				}
				return
			}
			if err != nil || actualRevision != 2 || queuedRevision != 2 {
				t.Fatal("pending queue left stale after internal flag", err, actualRevision, queuedRevision)
			}
			tr := state.Triggers[service.ID]
			if tr.Queued == nil || tr.Queued.InputSHA != queued.InputSHA || tr.Queued.RunID != queued.RunID || tr.Binding.Revision != 2 {
				t.Fatal("queued identity/input changed")
			}
			for _, tr := range state.Triggers {
				if tr.Phase == "ready" && tr.Binding.Revision != 2 {
					t.Fatal("future cron stale")
				}
			}
			if _, err = f.db.Exec(`UPDATE core_runs SET state='running' WHERE id=? AND state='pending'`, queued.RunID); err != nil {
				t.Fatal(err)
			}
			f.now = tr.Due
			if err = f.o.Execute(context.Background(), *tr.Queued); err != nil {
				t.Fatal("rebound service cannot execute", err)
			}
		})
	}
}
func TestNativeAcknowledgementAndCoreReceiptFailureNeverReissuesMutation(t *testing.T) {
	d := newDaemon(t, "qbittorrent")
	d.rows = []map[string]any{qbRow(badHash, "/daemon/a"), qbRow(goodHash, "/daemon/b")}
	f := newFixture(t, []Downloader{d.bind(t, "q")}, nil)
	f.config(t, map[string]any{"delete_invalid_torrents": true})
	f.c.failCommit = true
	inv := f.run(t, "native-sql-failed", "run", "")
	if err := f.o.Execute(context.Background(), inv); !errors.Is(err, ErrUnknown) {
		t.Fatal("daemon success/core SQL failure not uncertain", err)
	}
	d.mu.Lock()
	count := d.deletes[badHash]
	d.mu.Unlock()
	if count != 1 {
		t.Fatal("fixture did not really mutate daemon")
	}
	state, _ := f.j.Read(context.Background())
	found := false
	for _, effect := range state.Effects {
		if effect.Kind == "native_write" {
			found = true
			if effect.Phase != "started" || len(effect.NativeSnapshot) == 0 {
				t.Fatal("prewrite snapshot/intent lost")
			}
		}
	}
	if !found {
		t.Fatal("mutation had no durable intent")
	}
	f.c.failCommit = false
	reopened, err := Open(f.o.opts)
	if err != nil {
		t.Fatal(err)
	}
	if err = reopened.Execute(context.Background(), inv); !errors.Is(err, ErrUnknown) {
		t.Fatal("old native effect replayed", err)
	}
	d.mu.Lock()
	count = d.deletes[badHash]
	d.mu.Unlock()
	if count != 1 {
		t.Fatal("native call repeated on uncertain receipt")
	}
}
func TestInsertedTreeEntryCannotBeDeletedUsingOlderFrozenManifest(t *testing.T) {
	source, root := makeSource(t)
	tree := filepath.Join(source, "orphan-tree")
	if err := os.Mkdir(tree, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "owned.mkv"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	d := newDaemon(t, "qbittorrent")
	d.rows = []map[string]any{}
	f := newFixture(t, []Downloader{d.bind(t, "q")}, []Root{root})
	f.config(t, map[string]any{"detect_invalid_files": true, "delete_invalid_files": true, "download_dirs": source + ":" + source})
	added := filepath.Join(tree, "later.mkv")
	inserted := false
	f.c.before = func(effect Effect) {
		if effect.Kind == "file_delete" && !inserted {
			inserted = true
			if err := os.WriteFile(added, []byte("new"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	inv := f.run(t, "tree-insert", "run", "")
	if err := f.o.Execute(context.Background(), inv); err == nil {
		t.Fatal("new tree entry silently deleted")
	}
	for _, path := range []string{added, filepath.Join(tree, "owned.mkv")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("frozen tree mismatch deleted a file", path, err)
		}
	}
}

type loseCommittedConfigReply struct {
	Journal
	lost bool
}

func (j *loseCommittedConfigReply) Update(ctx context.Context, fn func(*sql.Tx, *State) error) error {
	lose := false
	err := j.Journal.Update(ctx, func(tx *sql.Tx, s *State) error {
		if err := fn(tx, s); err != nil {
			return err
		}
		if !j.lost {
			for _, effect := range s.Effects {
				if effect.Kind == "config_write" && effect.Phase == "confirmed" {
					lose = true
				}
			}
		}
		return nil
	})
	if err == nil && lose {
		j.lost = true
		return errors.New("synthetic commit acknowledgement loss")
	}
	return err
}
func TestOnlyOnceConfigCommitReplyLossUsesExactReceiptAndNeverSavesTwice(t *testing.T) {
	f := newFixture(t, nil, nil)
	wrapper := &loseCommittedConfigReply{Journal: f.j}
	f.o.opts.Journal = wrapper
	inv := f.run(t, "config-reply-lost", "init", "")
	if err := f.configure(t, inv, raw(map[string]any{"onlyonce": true, "enabled": false, "min_seeding_days": 0})); err != nil {
		t.Fatal("exact committed receipt was not recovered", err)
	}
	if !wrapper.lost {
		t.Fatal("fixture did not lose real commit reply")
	}
	state, err := f.j.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var receipts, revision int64
	_ = f.db.QueryRow(`SELECT count(*) FROM core_config_receipts`).Scan(&receipts)
	_ = f.db.QueryRow(`SELECT revision FROM core`).Scan(&revision)
	if receipts != 1 || revision != 2 || len(state.ConfigHistory) != 1 || state.Runs[inv.RunID].Invocation != inv || state.Runs[inv.RunID].Effective.Revision != 2 {
		t.Fatal("configuration was replayed or origin changed")
	}
	if _, found, err := f.o.Outcome(context.Background(), inv); err != nil || !found {
		t.Fatal("readback did not complete original invocation", found, err)
	}
}
func TestConfigRejectsInvalidUTF8AndMultipleObjectsBeforeDurableBegin(t *testing.T) {
	for _, input := range []json.RawMessage{[]byte("{\"label\":\"\xff\"}"), []byte(`{} {}`)} {
		f := newFixture(t, nil, nil)
		inv := f.run(t, "bad-config", "init", "")
		if err := f.o.Configure(context.Background(), inv, input); !errors.Is(err, ErrUnsafe) {
			t.Fatal("invalid config accepted", err)
		}
		state, err := f.j.Read(context.Background())
		if err != nil || len(state.Runs) != 0 {
			t.Fatal("invalid body entered owner", err)
		}
	}
}

func (f *fixture) configure(t *testing.T, inv Invocation, config json.RawMessage) error {
	t.Helper()
	if _, err := f.db.Exec(`UPDATE core SET config_sha=? WHERE instance=? AND generation=? AND revision=?`, digest(config), inv.Binding.InstanceID, inv.Binding.Generation, inv.Binding.Revision); err != nil {
		t.Fatal(err)
	}
	return f.o.Configure(context.Background(), inv, config)
}
func TestWarmCommandFailureRetainsTemporaryFlagsButColdOwnerUsesSavedConfiguration(t *testing.T) {
	d := newDaemon(t, "qbittorrent")
	d.rows = []map[string]any{}
	f := newFixture(t, []Downloader{d.bind(t, "q")}, nil)
	f.config(t, map[string]any{"delete_invalid_torrents": false, "exclude_categories": 123})
	command := publishFixtureCommand(t, f, "failed-delete-command", "delete_invalid_torrents")
	if err := f.o.Execute(context.Background(), command); err == nil {
		t.Fatal("synthetic source error absent")
	}
	state, _ := f.j.Read(context.Background())
	var saved map[string]any
	_ = json.Unmarshal(state.Config, &saved)
	if saved["delete_invalid_torrents"] != false || len(state.ConfigHistory) != 0 {
		t.Fatal("temporary runtime flags were falsely saved publicly")
	}
	warm := f.run(t, "warm-probe", "run", "")
	e, err := f.o.enter(context.Background(), warm, state.Config)
	if err != nil {
		t.Fatal(err)
	}
	if e.machine.Config["delete_invalid_torrents"] != true {
		t.Fatal("source no-finally residual state disappeared")
	}
	if err = e.finish(errors.New("probe completes without source work")); err == nil {
		t.Fatal("probe should be a source failure")
	}
	cold, err := Open(f.o.opts)
	if err != nil {
		t.Fatal(err)
	}
	next := f.run(t, "cold-probe", "run", "")
	e, err = cold.enter(context.Background(), next, state.Config)
	if err != nil {
		t.Fatal(err)
	}
	if e.machine.Config["delete_invalid_torrents"] != false {
		t.Fatal("cold owner inherited unsaved failed-command flags")
	}
	_ = e.finish(errors.New("probe completes without source work"))
}
func TestDuePlansCoalesceMisfireBusyAndRecheckExecutionGrace(t *testing.T) {
	t.Run("cron_coalesce_and_max_instances", func(t *testing.T) {
		f := newFixture(t, nil, nil)
		f.config(t, map[string]any{"cron": "* * * * *"})
		late := f.now.Add(10*24*time.Hour + 500*time.Millisecond)
		calls := 0
		var actual Trigger
		enqueue := func(tx *sql.Tx, tr Trigger) (Invocation, error) {
			calls++
			actual = tr
			inv := Invocation{Binding: tr.Binding, RunID: "coalesced", Action: "service", EventID: tr.ID, InputSHA: digest(tr.ID)}
			_, err := tx.Exec(`INSERT INTO core_runs VALUES(?,?,?,?,?,'pending')`, inv.RunID, inv.Binding.Generation, inv.Binding.Revision, inv.Action, inv.InputSHA)
			return inv, err
		}
		if err := f.o.ClaimDue(context.Background(), late, enqueue); err != nil || calls != 1 || !actual.Due.Equal(late.Add(-500*time.Millisecond)) {
			t.Fatal("outage accumulated old work", calls, actual.Due, err)
		}
		if err := f.o.ClaimDue(context.Background(), late, enqueue); err != nil || calls != 1 {
			t.Fatal("same due enqueued twice", err)
		}
		if err := f.o.ClaimDue(context.Background(), actual.Due.Add(time.Minute), enqueue); err != nil || calls != 1 {
			t.Fatal("busy source service overlapped", err)
		}
		state, _ := f.j.Read(context.Background())
		skipped := 0
		for _, tr := range state.Triggers {
			if tr.Phase == "skipped_busy" {
				skipped++
			}
		}
		if skipped != 1 {
			t.Fatal("busy occurrence fact missing", skipped)
		}
	})
	t.Run("late_once_is_missed_not_rescheduled", func(t *testing.T) {
		f := newFixture(t, nil, nil)
		f.config(t, map[string]any{"enabled": false, "onlyonce": true})
		calls := 0
		enqueue := func(*sql.Tx, Trigger) (Invocation, error) {
			calls++
			return Invocation{}, errors.New("must not enqueue")
		}
		if err := f.o.ClaimDue(context.Background(), f.now.Add(5*time.Second), enqueue); err != nil || calls != 0 {
			t.Fatal("expired once executed", calls, err)
		}
		state, _ := f.j.Read(context.Background())
		missed := 0
		for _, tr := range state.Triggers {
			if tr.Kind == "once" && tr.Phase == "missed" {
				missed++
			}
		}
		if missed != 1 {
			t.Fatal("date misfire lost")
		}
	})
	t.Run("late_execution_cannot_bypass_queue_grace", func(t *testing.T) {
		f := newFixture(t, nil, nil)
		f.config(t, map[string]any{"enabled": false, "onlyonce": true})
		due := f.now.Add(3 * time.Second)
		var inv Invocation
		if err := f.o.ClaimDue(context.Background(), due, func(tx *sql.Tx, tr Trigger) (Invocation, error) {
			inv = Invocation{Binding: tr.Binding, RunID: "late-execution", Action: "once", EventID: tr.ID, InputSHA: digest(tr.ID)}
			_, err := tx.Exec(`INSERT INTO core_runs VALUES(?,?,?,?,?,'running')`, inv.RunID, inv.Binding.Generation, inv.Binding.Revision, inv.Action, inv.InputSHA)
			return inv, err
		}); err != nil {
			t.Fatal(err)
		}
		f.now = due.Add(2 * time.Second)
		if err := f.o.Execute(context.Background(), inv); err != nil {
			t.Fatal(err)
		}
		state, _ := f.j.Read(context.Background())
		var result Result
		_ = json.Unmarshal(state.Runs[inv.RunID].Result, &result)
		if result.Skipped != "missed" || state.Triggers[inv.EventID].Phase != "missed" || len(state.Effects) != 1 {
			t.Fatal("late queued execution ran source cleanup", result)
		}
	})
}
func TestSuppliedConfigurationMustMatchTrustedCurrentRevision(t *testing.T) {
	f := newFixture(t, nil, nil)
	inv := f.run(t, "forged-config", "init", "")
	if err := f.o.Configure(context.Background(), inv, raw(map[string]any{"enabled": true})); !errors.Is(err, ErrUnsafe) {
		t.Fatal("same revision accepted worker-selected config", err)
	}
	state, _ := f.j.Read(context.Background())
	if len(state.Runs) != 0 {
		t.Fatal("forged config entered durable owner")
	}
}

func TestFalseyConfigurationPreservesOrdinaryObjectButReloadAndColdStartAreFresh(t *testing.T) {
	for _, kind := range []string{"ordinary_init", "reload", "cold_init"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t, nil, nil)
			f.config(t, map[string]any{"enabled": true})
			if kind == "cold_init" {
				owner, err := Open(f.o.opts)
				if err != nil {
					t.Fatal(err)
				}
				f.o = owner
			}
			action := "init"
			if kind == "reload" {
				action = "reload"
			}
			inv := f.run(t, "empty-config-"+kind, action, "")
			if err := f.configure(t, inv, json.RawMessage(`{}`)); err != nil {
				t.Fatal(err)
			}
			var live map[string]any
			_ = json.Unmarshal(f.o.runtimeConfig, &live)
			if live["enabled"] != (kind == "ordinary_init") {
				t.Fatal("source object identity was changed by empty config", kind, live["enabled"])
			}
		})
	}
}

func TestTreeReceiptsRetainOnlyEachDeletedEntryProof(t *testing.T) {
	source, root := makeSource(t)
	tree := filepath.Join(source, "many-owned-files")
	if err := os.Mkdir(tree, 0700); err != nil {
		t.Fatal(err)
	}
	// A long name set makes whole-manifest-per-leaf journal growth visible while
	// using only owned tiny files and the real descriptor/SQLite delete path.
	for i := 0; i < 80; i++ {
		name := strings.Repeat("a", 80) + string(rune('A'+i/26)) + string(rune('a'+i%26)) + ".mkv"
		if err := os.WriteFile(filepath.Join(tree, name), []byte("owned"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	d := newDaemon(t, "qbittorrent")
	d.rows = []map[string]any{}
	f := newFixture(t, []Downloader{d.bind(t, "q")}, []Root{root})
	f.config(t, map[string]any{"detect_invalid_files": true, "delete_invalid_files": true, "download_dirs": source + ":" + source})
	inv := f.run(t, "linear-tree-receipts", "run", "")
	if err := f.o.Execute(context.Background(), inv); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tree); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("tree was not actually removed", err)
	}
	state, err := f.j.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, effect := range state.Effects {
		if effect.Kind == "file_step" {
			count++
			if len(effect.ExpectedCore) != 1 || len(effect.Proof) > 1024 {
				t.Fatal("leaf receipt duplicates unrelated tree proof")
			}
		}
	}
	if count != 81 {
		t.Fatal("tree leaf/directory receipt missing", count)
	}
}
