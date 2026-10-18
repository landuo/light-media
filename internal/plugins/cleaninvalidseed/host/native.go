package seedhost

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"light-media/internal/download"
	seed "light-media/internal/plugins/cleaninvalidseed"
	"strings"
	"time"
)

func (e *execution) Services(_ context.Context, selected any) ([]seed.Service, error) {
	names := map[string]bool{}
	switch v := selected.(type) {
	case []any:
		for _, n := range v {
			s, ok := n.(string)
			if !ok {
				return nil, ErrUnsafe
			}
			names[s] = true
		}
	case []string:
		for _, n := range v {
			names[n] = true
		}
	default:
		return nil, ErrUnsafe
	}
	out := []seed.Service{}
	for _, d := range e.o.opts.Downloaders {
		if !names[d.Name] {
			continue
		}
		delete(names, d.Name)
		e.selected[d.Name] = d
		if d.Inactive {
			e.mu.Lock()
			e.readFailure = true
			e.mu.Unlock()
		}
		out = append(out, seed.Service{Name: d.Name, Kind: d.Kind, Inactive: d.Inactive})
	}
	if len(names) > 0 {
		return nil, ErrUnsafe
	}
	return out, nil
}
func (e *execution) native(s seed.Service) (seed.NativeDownloader, error) {
	d, ok := e.selected[s.Name]
	if !ok || d.Kind != s.Kind || d.Inactive {
		return seed.NativeDownloader{}, ErrUnsafe
	}
	return seed.NativeDownloader{Service: s, Call: func(ctx context.Context, op string, args map[string]any) (json.RawMessage, error) {
		return e.call(ctx, d, op, args)
	}}, nil
}
func (e *execution) Torrents(ctx context.Context, s seed.Service) ([]*seed.Object, bool, error) {
	n, err := e.native(s)
	if err != nil {
		return nil, true, err
	}
	rows, failed, err := n.List(ctx)
	e.mu.Lock()
	if failed || err != nil {
		e.readFailure = true
		e.hostErr = errors.Join(e.hostErr, ErrUnsafe)
	} else {
		e.lists[s.Name] = true
	}
	e.mu.Unlock()
	return rows, failed, err
}
func (e *execution) SetTags(ctx context.Context, s seed.Service, hash any, tags []any) (any, error) {
	n, err := e.native(s)
	if err != nil {
		return nil, err
	}
	return n.SetTags(ctx, hash, tags)
}
func (e *execution) DeleteTorrent(ctx context.Context, s seed.Service, hash any, deleteFiles bool) (any, error) {
	if deleteFiles {
		e.fault(ErrUnsafe)
		return nil, ErrUnsafe
	}
	n, err := e.native(s)
	if err != nil {
		return nil, err
	}
	return n.Delete(ctx, hash, false)
}
func selectedHash(v any) (string, error) {
	s, ok := v.(string)
	if !ok || (len(s) != 40 && len(s) != 64) {
		return "", ErrUnsafe
	}
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return "", ErrUnsafe
		}
	}
	return s, nil
}
func (e *execution) call(ctx context.Context, d Downloader, op string, args map[string]any) (result json.RawMessage, retErr error) {
	wantMutation := op == "qb.torrents_add_tags" || op == "qb.torrents_delete" || op == "tr.change_torrent" || op == "tr.remove_torrent"
	defer func() {
		if wantMutation && retErr != nil {
			e.fault(retErr)
		}
	}()

	if ctx.Err() != nil || e.ctx.Err() != nil {
		return nil, context.Canceled
	}
	prefix := "qb."
	if d.Kind == "transmission" {
		prefix = "tr."
	}
	if !strings.HasPrefix(op, prefix) {
		return nil, ErrUnsafe
	}
	var hash string
	var tags []string
	var mutation bool
	switch op {
	case "qb.torrents_info":
		if len(args) != 0 {
			return nil, ErrUnsafe
		}
	case "tr.torrent_get":
		if len(args) != 1 || digest(args["arguments"]) != digest(transmissionFields) {
			return nil, ErrUnsafe
		}
	case "qb.torrents_trackers":
		if len(args) != 1 {
			return nil, ErrUnsafe
		}
		var err error
		hash, err = selectedHash(args["torrent_hash"])
		if err != nil {
			return nil, err
		}
	case "qb.torrents_add_tags", "tr.change_torrent":
		if len(args) != 2 {
			return nil, ErrUnsafe
		}
		key, label := "torrent_hashes", "tags"
		if op == "tr.change_torrent" {
			key, label = "ids", "labels"
		}
		var err error
		hash, err = selectedHash(args[key])
		if err != nil {
			return nil, err
		}
		values, ok := args[label].([]any)
		if !ok {
			return nil, ErrUnsafe
		}
		for _, v := range values {
			tag, ok := v.(string)
			if !ok {
				return nil, ErrUnsafe
			}
			tags = append(tags, tag)
		}
		mutation = true
	case "qb.torrents_delete", "tr.remove_torrent":
		if len(args) != 2 {
			return nil, ErrUnsafe
		}
		key, flag := "torrent_hashes", "delete_files"
		if op == "tr.remove_torrent" {
			key, flag = "ids", "delete_data"
		}
		keep, ok := args[flag].(bool)
		if !ok || keep {
			return nil, ErrUnsafe
		}
		var err error
		hash, err = selectedHash(args[key])
		if err != nil {
			return nil, err
		}
		mutation = true
	default:
		return nil, ErrUnsafe
	}
	intent := e.scoped(Effect{Kind: "native_read", Operation: op, DownloaderID: d.ID, DownloaderRevision: d.Revision, DownloaderSHA: d.ConfigSHA, Args: raw(args)})
	if mutation {
		intent.Kind = "native_write"
		e.mu.Lock()
		snapshot, ok := e.nativeSnapshots[d.ID][hash]
		e.mu.Unlock()
		if !ok {
			e.fault(ErrUnsafe)
			return nil, ErrUnsafe
		}
		intent.NativeSnapshot = append(json.RawMessage(nil), snapshot...)
	}
	proof, release, err := e.o.opts.Core.Guard(ctx, e.inv, intent)
	if err != nil {
		return nil, err
	}
	if release == nil {
		return nil, ErrUnsafe
	}
	defer release()
	intent.Proof = proof
	if !mutation {
		if op == "qb.torrents_trackers" {
			return d.client.NativeTrackers(ctx, hash)
		}
		result, err := d.client.NativeTorrents(ctx)
		if err != nil {
			return nil, err
		}
		var rows []map[string]json.RawMessage
		decoder := json.NewDecoder(bytes.NewReader(result))
		if decoder.Decode(&rows) != nil {
			return nil, ErrUnsafe
		}
		snapshots := map[string]json.RawMessage{}
		key := "hash"
		if d.Kind == "transmission" {
			key = "hashString"
		}
		for _, row := range rows {
			var h string
			if json.Unmarshal(row[key], &h) == nil {
				if _, valid := selectedHash(h); valid == nil {
					snapshots[h] = raw(row)
				}
			}
		}
		e.mu.Lock()
		e.nativeSnapshots[d.ID] = snapshots
		e.mu.Unlock()
		return result, nil
	}
	e.ordinal++
	intent.ID = e.inv.RunID + ":native:" + fmt.Sprint(e.ordinal)
	intent.Kind = "native_write"
	intent.Phase = "started"
	if err = e.o.opts.Journal.Update(ctx, func(_ *sql.Tx, s *State) error {
		r, ok := s.Runs[e.inv.RunID]
		if !ok || r.Phase != "started" {
			return ErrAlready
		}
		if _, ok = s.Effects[intent.ID]; ok {
			return ErrAlready
		}
		s.Effects[intent.ID] = intent
		return nil
	}); err != nil {
		e.fault(err)
		return nil, err
	}
	if op == "qb.torrents_add_tags" || op == "tr.change_torrent" {
		err = d.client.SetTorrentTags(ctx, hash, tags)
	} else {
		err = d.client.Control(ctx, hash, "remove_keep_data")
	}
	intent.Phase = "confirmed"
	intent.Result = json.RawMessage(`{"protocol_acknowledged":true,"observed":false}`)
	commitCtx := ctx
	knownNotStarted := download.MutationNotStarted(err)
	if err != nil {
		if knownNotStarted {
			intent.ErrorCode = "DOWNLOADER_MUTATION_NOT_STARTED"
			intent.Result = json.RawMessage(`{"protocol_not_started":true}`)
			bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer cancel()
			commitCtx = nativeNotStartedContext(bounded, e.inv, intent, err)
		} else {
			intent.Phase = "unknown"
			intent.ErrorCode = "DOWNLOADER_MUTATION_UNCONFIRMED"
			intent.Result = nil
			e.fault(ErrUnknown)
		}
	}
	saveErr := e.persist(func(tx *sql.Tx, s *State) error {
		old, ok := s.Effects[intent.ID]
		if !ok || old.Phase != "started" || old.Operation != intent.Operation || old.DownloaderSHA != d.ConfigSHA {
			return ErrUnsafe
		}
		if err == nil || knownNotStarted {
			if commitErr := e.o.opts.Core.Commit(commitCtx, tx, e.inv, intent); commitErr != nil {
				return commitErr
			}
		}
		s.Effects[intent.ID] = intent
		return nil
	})
	if saveErr != nil {
		e.fault(ErrUnknown)
		return nil, ErrUnknown
	}
	return json.RawMessage(`null`), err
}

var transmissionFields = []string{"id", "name", "status", "labels", "hashString", "totalSize", "percentDone", "addedDate", "trackerList", "trackerStats", "leftUntilDone", "rateDownload", "rateUpload", "recheckProgress", "rateDownload", "rateUpload", "peersGettingFromUs", "peersSendingToUs", "uploadRatio", "uploadedEver", "downloadedEver", "downloadDir", "error", "errorString", "doneDate", "queuePosition", "activityDate", "trackers"}
