package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"unicode/utf8"
)

// QBittorrentCompletedQueryCapability grants complete completed-task fields
// from one explicitly enabled host configuration, including potentially private
// tracker metadata. It grants neither credentials, arbitrary RPC nor mutation.
const QBittorrentCompletedQueryCapability = "downloader.qbittorrent.completed.query"

// IDs and Tags preserve the source None/string/list unions. IDs=nil enumerates
// completed tasks without prior hashes; [] sends the source empty hashes value,
// NOT the empty-selection meaning of downloader.tasks.read. List elements are
// retained as JSON values so the source's str/set behavior can be represented.
type QBittorrentCompletedSelection struct {
	DownloaderID string `json:"downloader_id"`
	IDs          any    `json:"ids"`
	Tags         any    `json:"tags"`
}

type QBittorrentTorrent struct {
	Fields map[string]json.RawMessage `json:"fields"`
}

func (t QBittorrentTorrent) DecodeField(name string, output any) error {
	raw, ok := t.Fields[name]
	if !ok {
		return errors.New("qBittorrent field is absent")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(output); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("invalid qBittorrent field")
	}
	return nil
}

type QBittorrentCompletedResult struct {
	SchemaVersion int                  `json:"schema_version"`
	DownloaderID  string               `json:"downloader_id"`
	Torrents      []QBittorrentTorrent `json:"torrents"`
	// Error is the source get_torrents caught exception / disconnected branch.
	// A completion-predicate exception or host scope error is a Go error instead.
	Error bool `json:"error"`
}

// Construct this finite client from the current invocation's HostCaller. A
// retained callback is not a reusable background credential.
type QBittorrent struct{ Host HostCaller }

func (q QBittorrent) GetCompletedTorrents(ctx context.Context, selection QBittorrentCompletedSelection) ([]QBittorrentTorrent, error) {
	out, err := q.QueryCompletedTorrents(ctx, selection)
	if err != nil || out.Error {
		return nil, err // source None is distinct from successful nonnil [].
	}
	return out.Torrents, nil
}

func (q QBittorrent) QueryCompletedTorrents(ctx context.Context, selection QBittorrentCompletedSelection) (QBittorrentCompletedResult, error) {
	var out QBittorrentCompletedResult
	if q.Host == nil {
		return out, errors.New("qBittorrent completed query requires an active host caller")
	}
	if !qbittorrentCompletedUnicode(reflect.ValueOf(selection), 0) {
		return out, errors.New("qBittorrent query requires valid Unicode strings")
	}
	raw, err := json.Marshal(selection)
	if err != nil {
		return out, err
	}
	raw, err = q.Host(ctx, QBittorrentCompletedQueryCapability, raw)
	if err != nil {
		return out, err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err = d.Decode(&out); err != nil {
		return QBittorrentCompletedResult{}, err
	}
	if d.Decode(new(any)) != io.EOF || out.SchemaVersion != 1 || out.DownloaderID != selection.DownloaderID || out.Torrents == nil || out.Error && len(out.Torrents) != 0 {
		return QBittorrentCompletedResult{}, errors.New("invalid qBittorrent completed response")
	}
	for _, torrent := range out.Torrents {
		if torrent.Fields == nil {
			return QBittorrentCompletedResult{}, errors.New("invalid qBittorrent task fields")
		}
	}
	return out, nil
}

func qbittorrentCompletedUnicode(value reflect.Value, depth int) bool {
	if depth > 64 {
		return false
	}
	if !value.IsValid() {
		return true
	}
	switch value.Kind() {
	case reflect.String:
		return utf8.ValidString(value.String())
	case reflect.Interface, reflect.Pointer:
		return value.IsNil() || qbittorrentCompletedUnicode(value.Elem(), depth+1)
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			if !qbittorrentCompletedUnicode(value.Index(i), depth+1) {
				return false
			}
		}
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			if !qbittorrentCompletedUnicode(value.Field(i), depth+1) {
				return false
			}
		}
	case reflect.Map:
		for _, key := range value.MapKeys() {
			if !qbittorrentCompletedUnicode(key, depth+1) || !qbittorrentCompletedUnicode(value.MapIndex(key), depth+1) {
				return false
			}
		}
	}
	return true
}
