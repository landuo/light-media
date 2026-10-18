package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	seedhost "light-media/internal/plugins/cleaninvalidseed/host"
	"light-media/internal/plugins/schedule"
)

// The callback runs inside both instance and host action gates. It acquires
// real App file-write locks once and releases global lifecycle before network
// work. All later Core operations deliberately avoid lifecycle/pluginMu.
func (o *pluginCleanInvalidSeedOwner) beforeRun(ctx context.Context, inv seedhost.Invocation) (seedhost.Options, func(), error) {
	options := seedhost.Options{}
	call, ok := ctx.Value(pluginCleanInvalidSeedCallKey{}).(*pluginCleanInvalidSeedCall)
	if !ok || call == nil || call.owner != o || call.inv != inv || call.nonce == "" || ctx.Err() != nil {
		return options, nil, seedhost.ErrUnsafe
	}
	o.a.pluginLifecycleMu.Lock()
	lifeHeld := true
	fileHeld := false
	ok = false
	defer func() {
		if !ok {
			if fileHeld {
				o.a.importMu.Unlock()
				o.a.downloadMu.Unlock()
			}
			if lifeHeld {
				o.a.pluginLifecycleMu.Unlock()
			}
		}
	}()
	o.a.pluginMu.Lock()
	i, err := o.a.pluginStore.Installation(ctx, o.instance)
	if err == nil {
		err = cleanSeedInstalled(i)
	}
	c := PluginStoredValue{}
	if err == nil {
		c, err = o.a.pluginStore.Config(ctx, o.instance)
	}
	if err == nil && (i.Generation != o.generation || !i.DesiredEnabled || c.Revision != inv.Binding.Revision || !bytes.Equal(c.Value, call.config)) {
		err = seedhost.ErrUnsafe
	}
	o.a.pluginMu.Unlock()
	if err != nil {
		return options, nil, err
	}
	o.a.downloadMu.Lock()
	o.a.importMu.Lock()
	fileHeld = true
	if err = ctx.Err(); err != nil {
		return options, nil, err
	}
	settings := o.a.Settings() // after the real Settings publication/file barrier
	tx, err := o.a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return options, nil, err
	}
	s, m, err := o.j.read(ctx, tx)
	if err == nil {
		for _, r := range s.Runs {
			if r.Phase == "started" || r.Phase == "unknown" {
				err = seedhost.ErrUnknown
				break
			}
		}
		for _, e := range s.Effects {
			if e.Phase == "started" || e.Phase == "unknown" || e.Phase == "fs_confirmed" {
				err = seedhost.ErrUnknown
				break
			}
		}
	}
	if err == nil && m.Transition != nil && m.Transition.Phase != "applied" {
		t := m.Transition
		if t.Phase != "published" || t.To != inv.Binding || t.RunID != inv.RunID || t.Kind != inv.Action {
			err = seedhost.ErrUnknown
		}
	}
	coldClosedInit := m.Closed && inv.Action == "init" && m.AppliedBinding == inv.Binding
	publishedInit := m.Transition != nil && m.Transition.Phase == "published" && m.Transition.RunID == inv.RunID && m.Transition.To == inv.Binding && m.Transition.Kind == inv.Action
	if err == nil && (inv.Action == "init" || inv.Action == "reload") && s.Binding != (seedhost.Binding{}) && !publishedInit && !coldClosedInit {
		err = seedhost.ErrUnsafe
	}
	if err == nil {
		var actual PluginRun
		actual, err = o.a.pluginStore.scanPluginRun(tx.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, inv.RunID))
		if err == nil {
			got, e := cleanSeedInvocation(actual)
			if e != nil || got != inv || actual.State != "running" || !bytes.Equal(actual.Input, call.run.Input) {
				err = seedhost.ErrUnsafe
			}
		}
	}
	if err == nil {
		var current int
		err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_installations i JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.instance_id=? AND i.plugin_id='CleanInvalidSeed' AND i.version='2.1' AND i.installed=1 AND i.desired_enabled=1 AND i.generation=? AND v.revision=?`, o.instance, o.generation, inv.Binding.Revision).Scan(&current)
		if err == nil && current != 1 {
			err = seedhost.ErrUnsafe
		}
	}
	_ = tx.Rollback()
	if err != nil {
		return options, nil, err
	}
	roots, err := cleanSeedRoots(settings, call.effective)
	if err != nil {
		return options, nil, err
	}
	refs := make([]pluginCleanInvalidSeedDownloaderRef, 0, len(settings.Downloaders))
	clients := make([]seedhost.Downloader, 0, len(settings.Downloaders))
	for _, d := range settings.Downloaders {
		sha := cleanSeedDownloaderSHA(o.j, d)
		revision := int64(1)
		if m.Snapshot != nil {
			for _, prior := range m.Snapshot.Downloaders {
				if prior.ID == d.ID {
					revision = prior.Revision
					if prior.ConfigSHA != sha {
						revision++
					}
					break
				}
			}
		}
		client, e := seedhost.BindDownloader(d.Name, revision, sha, d.Config)
		if e != nil {
			return options, nil, e
		}
		client.Inactive = !downloaderEnabled(d)
		clients = append(clients, client)
		refs = append(refs, pluginCleanInvalidSeedDownloaderRef{ID: d.ID, Name: d.Name, Kind: client.Kind, Revision: revision, ConfigSHA: sha, Inactive: client.Inactive})
	}
	snapshot := pluginCleanInvalidSeedSnapshot{Binding: inv.Binding, ConfigSHA: tokenHash(string(call.config)), Downloaders: refs, Roots: roots}
	l := &pluginCleanInvalidSeedLease{call: call, nonce: call.nonce, snapshot: snapshot, downloaders: clients, active: true, grants: map[string]int{}}
	call.mu.Lock()
	if call.lease != nil {
		call.mu.Unlock()
		return options, nil, seedhost.ErrUnsafe
	}
	call.lease = l
	call.mu.Unlock()
	var once sync.Once
	release := func() {
		once.Do(func() {
			l.mu.Lock()
			l.active = false
			l.nonce = ""
			l.mu.Unlock()
			call.mu.Lock()
			if call.lease == l {
				call.lease = nil
			}
			call.mu.Unlock()
			o.a.importMu.Unlock()
			o.a.downloadMu.Unlock()
		})
	}
	// Snapshot publication is private and occurs before source admission. It
	// preserves prior receipts and cannot advance the public configuration.
	tx, err = o.a.Store.DB.BeginTx(ctx, nil)
	if err == nil {
		err = o.j.updateTx(ctx, tx, func(current *seedhost.State, meta *pluginCleanInvalidSeedMeta) error {
			meta.Snapshot = &snapshot
			meta.Closed = false
			return cleanSeedClaimOccurrenceTx(ctx, tx, *current, inv)
		})
		if err == nil {
			err = tx.Commit()
		}
		_ = tx.Rollback()
	}
	if err != nil {
		call.mu.Lock()
		call.lease = nil
		call.mu.Unlock()
		l.active = false
		return options, nil, err
	}
	o.a.pluginLifecycleMu.Unlock()
	lifeHeld = false
	options = seedhost.Options{Binding: inv.Binding, Journal: o.journal, Core: o.core, BeforeRun: o.beforeRun, Roots: roots, Downloaders: clients, Timezone: "Asia/Shanghai", Now: o.a.pluginCleanInvalidSeed.now}
	ok = true
	return options, release, nil
}

// Permissions follow configured actual source directories. An allowed parent
// is not itself a cleanup root. Media roots retain their actual core IDs.
func cleanSeedRoots(settings Settings, raw json.RawMessage) ([]seedhost.Root, error) {
	c, err := cleanSeedConfigMap(raw)
	if err != nil {
		return nil, err
	}
	text, ok := c["download_dirs"].(string)
	if !ok && c["download_dirs"] != nil {
		return nil, seedhost.ErrUnsafe
	}
	paths := []string{}
	for _, line := range strings.Split(text, "\n") {
		before, _, ok := strings.Cut(line, ":")
		if ok {
			p := strings.TrimSpace(before)
			if p != "" {
				paths = append(paths, p)
			}
		}
	}
	// Age checks read real torrent paths. Only configured actual downloader
	// directories can add this read authority; list payloads cannot add roots.
	if days := c["min_seeding_days"]; days != nil && days != json.Number("0") && days != float64(0) {
		for _, d := range settings.Downloaders {
			if downloaderEnabled(d) && d.DownloadPath != "" {
				paths = append(paths, d.DownloadPath)
			}
		}
	}
	out := []seedhost.Root{}
	for _, path := range paths {
		if !importPath(path) || filepath.Clean(path) != path || path == "/" {
			return nil, seedhost.ErrUnsafe
		}
		id, role, authority := "seed_source_"+tokenHash(path), "source", path
		for _, m := range settings.Roots {
			if within(m.Path, path) {
				id, role, authority = m.ID, "media", m.Path
				break
			}
		}
		if role == "source" {
			allowed := false
			for _, p := range settings.AllowedRoots {
				if within(p, path) {
					allowed = true
					break
				}
			}
			if !allowed {
				return nil, seedhost.ErrUnsafe
			}
		}
		duplicate := false
		for _, old := range out {
			if old.Path == authority && old.ID == id {
				duplicate = true
				break
			}
			if within(old.Path, authority) || within(authority, old.Path) {
				return nil, bad("CLEAN_SEED_ROOT_SCOPE_CONFLICT", "下载目录不能跨越媒体库与源目录的授权边界，请选择实际独立子目录")
			}
		}
		if duplicate {
			continue
		}
		root, err := seedhost.AuthorizeRoot(id, authority, role)
		if err != nil {
			return nil, err
		}
		out = append(out, root)
	}
	return out, nil
}

// The wrapper adds actual core schedule projection to the same existing SQL
// transaction; it creates neither a database nor an independent job system.
type pluginCleanInvalidSeedActionJournal struct {
	o           *pluginCleanInvalidSeedOwner
	afterCommit func() error
}

func (j *pluginCleanInvalidSeedActionJournal) Read(ctx context.Context) (seedhost.State, error) {
	return j.o.j.Read(ctx)
}
func (j *pluginCleanInvalidSeedActionJournal) Update(ctx context.Context, fn func(*sql.Tx, *seedhost.State) error) error {
	tx, err := j.o.a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	err = j.o.j.updateTx(ctx, tx, func(s *seedhost.State, m *pluginCleanInvalidSeedMeta) error {
		if err := fn(tx, s); err != nil {
			return err
		}
		if owner, ok := ctx.Value(pluginCleanInvalidSeedStopKey{}).(*pluginCleanInvalidSeedOwner); ok && owner == j.o {
			m.Closed = true
		}
		if call, ok := ctx.Value(pluginCleanInvalidSeedCallKey{}).(*pluginCleanInvalidSeedCall); ok && call != nil && call.owner == j.o {
			if err := cleanSeedAdvanceAppliedBindingTx(ctx, tx, j.o.j, *s, m, call.inv); err != nil {
				return err
			}
			if r, found := s.Runs[call.run.ID]; found && (r.Phase == "confirmed" || r.Phase == "failed") && (call.run.Action == "init" || call.run.Action == "reload") {
				if m.Transition != nil && m.Transition.Phase != "applied" {
					if m.Transition.RunID != call.run.ID || m.Transition.To != call.inv.Binding {
						return seedhost.ErrUnsafe
					}
					m.Transition.Phase = "applied"
				}
				m.AppliedRun = call.run.ID
				m.AppliedBinding = r.Effective
			}
			if call.inv.EventID != "" {
				if tr, found := s.Triggers[call.inv.EventID]; found && tr.Phase == "missed" && tr.OccurrenceID != "" {
					if _, err := tx.ExecContext(ctx, `UPDATE plugin_schedule_occurrences SET state='missed' WHERE id=? AND run_id=? AND state='running'`, tr.OccurrenceID, call.run.ID); err != nil {
						return err
					}
				}
			}
		}
		return j.o.projectSchedulesTx(ctx, tx, *s)
	})
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	// Native storage seam for a lost acknowledgement after an actual commit.
	if j.afterCommit != nil {
		return j.afterCommit()
	}
	return nil
}
func cleanSeedServiceID(plan string) string { return "clean_seed." + tokenHash(plan) }
func (o *pluginCleanInvalidSeedOwner) projectSchedulesTx(ctx context.Context, tx *sql.Tx, s seedhost.State) error {
	var generation, revision int64
	var installed, enabled bool
	err := tx.QueryRowContext(ctx, `SELECT i.generation,i.installed,i.desired_enabled,COALESCE(v.revision,0) FROM plugin_installations i LEFT JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.instance_id=?`, o.instance).Scan(&generation, &installed, &enabled, &revision)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE plugin_schedules SET active=0,updated_at=? WHERE instance_id=? AND group_key='clean_seed'`, now(), o.instance); err != nil {
		return err
	}
	if !installed || !enabled || generation != o.generation {
		return nil
	}
	ids := []string{}
	for id, t := range s.Triggers {
		if t.Phase == "ready" && (t.Kind == "once" || t.Kind == "service") && t.Binding == (seedhost.Binding{InstanceID: o.instance, Generation: generation, Revision: revision}) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		t := s.Triggers[id]
		spec := schedule.Spec{Kind: "date", Timezone: "Asia/Shanghai", RunAt: schedule.FormatTime(t.Due)}
		if t.Kind == "service" {
			if t.Spec == nil {
				return seedhost.ErrUnsafe
			}
			spec = *t.Spec
		}
		input, _ := json.Marshal(map[string]string{"source_plan_id": id})
		def := pluginScheduleDefinition{ID: cleanSeedServiceID(id), Name: "清理无效做种", Handler: "clean_invalid_seed", Trigger: spec, Input: input, MisfireGraceMS: 1000}
		raw, err := pluginJSON(def, true)
		if err != nil {
			return err
		}
		digest := tokenHash(string(raw))
		sealed, err := o.a.pluginStore.seal("schedule", o.instance, def.ID, raw)
		if err != nil {
			return err
		}
		var oldDigest, group string
		var oldGen int64
		var due sql.NullString
		err = tx.QueryRowContext(ctx, `SELECT generation,group_key,definition_digest,next_due FROM plugin_schedules WHERE instance_id=? AND service_id=?`, o.instance, def.ID).Scan(&oldGen, &group, &oldDigest, &due)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && group != "clean_seed" {
			return seedhost.ErrUnsafe
		}
		var next any = schedule.FormatTime(t.Due)
		if err == nil && oldGen == generation && oldDigest == digest {
			if due.Valid {
				next = due.String
			} else {
				next = nil
			}
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_schedules VALUES(?,?,?,?,?,?,?,1,?,?) ON CONFLICT(instance_id,service_id) DO UPDATE SET generation=excluded.generation,config_revision=excluded.config_revision,definition=excluded.definition,definition_digest=excluded.definition_digest,active=1,next_due=excluded.next_due,updated_at=excluded.updated_at`, o.instance, def.ID, "clean_seed", generation, revision, sealed, digest, next, now()); err != nil {
			return err
		}
	}
	return nil
}

var _ seedhost.Journal = (*pluginCleanInvalidSeedActionJournal)(nil)
