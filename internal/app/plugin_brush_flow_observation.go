package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	bf "light-media/internal/plugins/brushflow"
	native "light-media/internal/plugins/iyuuautoseed/native"
	"path"
	"strings"
)

type brushFlowAddIntent struct {
	Hash          string                  `json:"hash"`
	Task          string                  `json:"task_id"`
	Downloader    string                  `json:"downloader_name"`
	DownloaderID  string                  `json:"downloader_id"`
	DownloaderSHA string                  `json:"downloader_sha"`
	SavePath      *string                 `json:"save_path"`
	Category      string                  `json:"category"`
	Tags          []string                `json:"tags"`
	Record        bf.Row                  `json:"record"`
	Destination   *native.IYUUDestination `json:"destination"`
}

func (o *pluginBrushFlowOwner) reconcileAdds(ctx context.Context) error {
	// The owner gate is held by the real deferred caller. Only a last, unresolved
	// add can be observed. Never replay its write, tag cleanup, limits or event.
	s, _, e := o.a.pluginStore.brushFlowRead(ctx, o.a.Store.DB, o.instance, o.generation)
	if e != nil {
		return e
	}
	for id, r := range s.Runs {
		if r.Phase != "started" || len(r.Effects) == 0 {
			continue
		}
		ordinal := len(r.Effects) - 1
		f := r.Effects[ordinal]
		if f.Kind != "add_torrent" || f.Phase != "started" {
			continue
		}
		prior := true
		for _, old := range r.Effects[:ordinal] {
			prior = prior && old.Phase == "confirmed"
		}
		if !prior {
			continue
		}
		var intent brushFlowAddIntent
		decoder := json.NewDecoder(bytes.NewReader(f.Args))
		decoder.UseNumber()
		if decoder.Decode(&intent) != nil || intent.Hash == "" || intent.Task == "" || intent.Record["task_id"] != intent.Task {
			return brushFlowEvidenceError()
		}
		d, e := seedRescueDownloader(o.a.Settings(), intent.Downloader)
		if e != nil || d.ID != intent.DownloaderID || seedRescueDownloaderSHA(d) != intent.DownloaderSHA {
			continue
		}
		guard := func(ctx context.Context) error { return o.brushFlowObservationGuard(ctx, id, ordinal, f, d) }
		if e = guard(ctx); e != nil {
			return e
		}
		client, e := native.NewIYUUClient(seedRescueNativeConfig(d), native.IYUUOptions{APIKey: d.APIKey, AutoCategory: d.AutoCategory, Sequential: d.Sequential, FirstLast: d.FirstLast, Guard: guard})
		if e != nil {
			return e
		}
		raw, e := client.Torrents(ctx, []string{intent.Hash}, false)
		client.Close()
		if e != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		var rows []bf.Row
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if dec.Decode(&rows) != nil {
			continue
		}
		matched := false
		for _, row := range rows {
			kind := seedRescueNativeConfig(d).Kind
			v := bf.DownloadState{Kind: kind, Raw: row}
			if !strings.EqualFold(brushFlowHash(v), intent.Hash) {
				continue
			}
			labels := brushFlowLabels(v)
			if !brushFlowHas(labels, bf.GlobalTag) || !brushFlowHas(labels, (bf.Task{Row: bf.Row{"id": intent.Task}}).Tag()) {
				continue
			}
			if kind == "qbittorrent" && (len(intent.Tags) != 4 || !brushFlowHas(labels, intent.Tags[3])) {
				continue
			}
			want := ""
			if intent.Destination != nil {
				want = intent.Destination.EffectivePath
			} else if intent.SavePath != nil {
				want = *intent.SavePath
			}
			actual := brushFlowString(row["save_path"])
			if kind == "transmission" {
				actual = brushFlowString(row["download_dir"])
				if actual == "" {
					actual = brushFlowString(row["downloadDir"])
				}
			}
			if want == "" || actual == "" || path.Clean(want) != path.Clean(actual) {
				continue
			}
			if kind == "qbittorrent" {
				if category, ok := row["category"].(string); !ok || category != intent.Category {
					continue
				}
				if intent.Destination != nil {
					auto, ok := row["auto_tmm"].(bool)
					if !ok || auto != intent.Destination.AutoTMM {
						continue
					}
				}
			}
			matched = true
			break
		}
		if matched {
			if e = o.confirmBrushFlowObservation(ctx, id, ordinal, f, intent, d); e != nil {
				return e
			}
		}
	}
	s, _, e = o.a.pluginStore.brushFlowRead(ctx, o.a.Store.DB, o.instance, o.generation)
	if e != nil {
		return e
	}
	return brushFlowAdmissionState(ctx, o.a.pluginStore, s)
}
func (o *pluginBrushFlowOwner) brushFlowObservationGuard(ctx context.Context, run string, ordinal int, f brushFlowEffect, d DownloaderConfig) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	current, e := seedRescueDownloader(o.a.Settings(), d.Name)
	if e != nil || seedRescueDownloaderSHA(current) != seedRescueDownloaderSHA(d) {
		return brushFlowEvidenceError()
	}
	tx, e := o.a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return e
	}
	defer tx.Rollback()
	return o.brushFlowObservationGuardTx(ctx, tx, run, ordinal, f)
}
func (o *pluginBrushFlowOwner) brushFlowObservationGuardTx(ctx context.Context, tx *sql.Tx, run string, ordinal int, f brushFlowEffect) error {
	s, _, e := o.a.pluginStore.brushFlowRead(ctx, tx, o.instance, o.generation)
	if e != nil {
		return e
	}
	r, ok := s.Runs[run]
	if !ok || r.Phase != "started" || !r.Entered || ordinal != len(r.Effects)-1 || ordinal < 0 || !bytes.Equal(mustCleanSeedJSON(r.Effects[ordinal]), mustCleanSeedJSON(f)) {
		return brushFlowEvidenceError()
	}
	var plugin, version string
	var gen, rev int64
	var installed, enabled bool
	e = tx.QueryRowContext(ctx, `SELECT i.plugin_id,i.version,i.generation,i.installed,i.desired_enabled,v.revision FROM plugin_installations i JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.instance_id=?`, o.instance).Scan(&plugin, &version, &gen, &installed, &enabled, &rev)
	if e != nil || plugin != bf.PluginID || version != bf.Version || gen != o.generation || !installed || !enabled || rev != r.EffectiveRevision {
		return brushFlowEvidenceError()
	}
	return nil
}
func (o *pluginBrushFlowOwner) confirmBrushFlowObservation(ctx context.Context, id string, ordinal int, f brushFlowEffect, intent brushFlowAddIntent, d DownloaderConfig) error {
	finish, cancel := seedRescueFinishContext(ctx)
	defer cancel()
	tx, e := o.a.Store.DB.BeginTx(finish, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = o.brushFlowObservationGuardTx(finish, tx, id, ordinal, f); e != nil {
		return e
	}
	current, e := seedRescueDownloader(o.a.Settings(), d.Name)
	if e != nil || seedRescueDownloaderSHA(current) != intent.DownloaderSHA {
		return brushFlowEvidenceError()
	}
	p := o.a.pluginStore
	s, b, e := p.brushFlowRead(finish, tx, o.instance, o.generation)
	if e != nil {
		return e
	}
	run, e := p.scanPluginRun(tx.QueryRowContext(finish, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, id))
	if e != nil {
		return e
	}
	deferred, previous, e := p.readDeferred(finish, tx, run)
	if e != nil || deferred.Phase != "started" && deferred.Phase != "uncertain" {
		return brushFlowEvidenceError()
	}
	key := "task." + intent.Task + ".torrents"
	records := bf.NewRecords()
	if _, ok := s.Values[key]; ok {
		var sealed []byte
		if e = tx.QueryRowContext(finish, `SELECT sealed FROM plugin_values WHERE instance_id=? AND kind='data' AND item_key=?`, o.instance, key).Scan(&sealed); e != nil {
			return e
		}
		raw, e := p.unseal("data", o.instance, key, sealed)
		if e != nil || records.UnmarshalJSON(raw) != nil {
			return brushFlowEvidenceError()
		}
	}
	records.Set(intent.Hash, intent.Record)
	raw, e := json.Marshal(records)
	if e != nil {
		return e
	}
	call := brushFlowCall{o: o}
	if e = call.writeValueTx(finish, tx, &s, key, raw); e != nil {
		return e
	}
	r := s.Runs[id]
	r.Effects[ordinal].Phase = "confirmed"
	r.Effects[ordinal].Result = mustCleanSeedJSON(bf.Row{"observed": true, "hash": intent.Hash, "write_replayed": false})
	r.Phase = "failed"
	r.ErrorCode = "BRUSH_FLOW_OBSERVED"
	r.Result = mustCleanSeedJSON(bf.Row{"decision": "observed_without_replay", "hash": intent.Hash, "effects_replayed": false, "plugin_triggered_emitted": false})
	s.Runs[id] = r
	if e = p.brushFlowSave(finish, tx, s, b); e != nil {
		return e
	}
	deferred.Phase = "terminal_failed"
	deferred.ErrorCode = r.ErrorCode
	deferred.Result = r.Result
	if e = p.writeDeferred(finish, tx, deferred, previous); e != nil {
		return e
	}
	sealed, e := p.seal("run_result", o.instance, id, r.Result)
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(finish, `UPDATE plugin_runs SET state='failed',error_code=?,result=?,updated_at=? WHERE id=?`, r.ErrorCode, sealed, now(), id); e != nil {
		return e
	}
	if _, e = tx.ExecContext(finish, `UPDATE plugin_schedule_occurrences SET state='failed' WHERE run_id=? AND state IN ('running','reconcile_pending')`, id); e != nil {
		return e
	}
	return tx.Commit()
}
