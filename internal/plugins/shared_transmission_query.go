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

// TransmissionTorrentsQueryCapability authorizes complete task fields from one
// explicitly configured Transmission instance. Unlike downloader.tasks.read,
// this includes tracker URLs/private task metadata. It grants no credentials,
// arbitrary RPC, mutation, or qBittorrent access. Do not log returned fields.
const TransmissionTorrentsQueryCapability = "downloader.transmission.torrents.query"

// TransmissionQuery preserves the original get_torrents unions. IDs accepts
// nil, a numeric ID, a SHA1 string, "recently-active", or a list of IDs/hashes.
// nil and an empty list both enumerate ALL tasks (the source vendor behavior).
// Status and Tags accept nil, a string, or []string. Tags strings are split on
// commas without trimming. Status names use spaces, e.g. "download pending".
// Invalid native ID values produce Error=true, as in the source wrapper; an
// invalid wire object or revoked invocation produces a Go error instead.
type TransmissionQuery struct {
	DownloaderID string `json:"downloader_id"`
	IDs          any    `json:"ids"`
	Status       any    `json:"status"`
	Tags         any    `json:"tags"`
}

// TransmissionTorrent contains the complete source _trarg response, in daemon
// order, with native field names and exact numeric lexemes. This is the Go
// counterpart of transmission_rpc.Torrent.fields, not the narrow UI Task DTO.
type TransmissionTorrent struct {
	Fields map[string]json.RawMessage `json:"fields"`
}

func (t TransmissionTorrent) DecodeField(name string, output any) error {
	raw, ok := t.Fields[name]
	if !ok {
		return errors.New("transmission field is absent")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	return d.Decode(output)
}

func (t TransmissionTorrent) HashString() (string, error) {
	var out string
	err := t.DecodeField("hashString", &out)
	return out, err
}
func (t TransmissionTorrent) Name() (string, error) {
	var out string
	err := t.DecodeField("name", &out)
	return out, err
}
func (t TransmissionTorrent) Labels() ([]string, error) {
	var out []string
	err := t.DecodeField("labels", &out)
	return out, err
}
func (t TransmissionTorrent) Status() (string, error) {
	var code int
	if err := t.DecodeField("status", &code); err != nil {
		return "", err
	}
	values := [...]string{"stopped", "check pending", "checking", "download pending", "downloading", "seed pending", "seeding"}
	if code < 0 || code >= len(values) {
		return "", errors.New("unknown transmission status")
	}
	return values[code], nil
}

type TransmissionQueryResult struct {
	SchemaVersion int                   `json:"schema_version"`
	DownloaderID  string                `json:"downloader_id"`
	Torrents      []TransmissionTorrent `json:"torrents"`
	Error         bool                  `json:"error"`
}

// Transmission returns only finite query operations. Construct it from this
// invocation's HostCaller; a retained caller is not a background credential.
type Transmission struct{ Host HostCaller }

type TransmissionSelection struct {
	DownloaderID string
	IDs          any
	Tags         any
}

// These are local wrappers over the same finite query grant. A nil slice with
// nil Go error is the original source None on a daemon error; a nonnil empty
// slice is a successful query without matches. Keep the pinned wrapper's exact
// underscore spellings, even though the vendor's pending statuses use spaces.
func (t Transmission) GetCompletedTorrents(ctx context.Context, selection TransmissionSelection) ([]TransmissionTorrent, error) {
	return t.selectedTorrents(ctx, selection, []string{"seeding", "seed_pending"})
}
func (t Transmission) GetDownloadingTorrents(ctx context.Context, selection TransmissionSelection) ([]TransmissionTorrent, error) {
	return t.selectedTorrents(ctx, selection, []string{"downloading", "download_pending"})
}
func (t Transmission) selectedTorrents(ctx context.Context, selection TransmissionSelection, status []string) ([]TransmissionTorrent, error) {
	result, err := t.GetTorrents(ctx, TransmissionQuery{DownloaderID: selection.DownloaderID, IDs: selection.IDs, Tags: selection.Tags, Status: status})
	if err != nil || result.Error {
		return nil, err
	}
	return result.Torrents, nil
}

func (t Transmission) GetTorrents(ctx context.Context, request TransmissionQuery) (TransmissionQueryResult, error) {
	var out TransmissionQueryResult
	if t.Host == nil {
		return out, errors.New("transmission query requires an active host caller")
	}
	// encoding/json replaces invalid Go string bytes. Refuse that lossy input
	// before marshalling, including strings inside source union/list arguments.
	if !transmissionQueryUnicode(reflect.ValueOf(request), 0) {
		return out, errors.New("transmission query requires valid Unicode strings")
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return out, err
	}
	raw, err = t.Host(ctx, TransmissionTorrentsQueryCapability, raw)
	if err != nil {
		return out, err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err = d.Decode(&out); err != nil {
		return TransmissionQueryResult{}, err
	}
	if d.Decode(new(any)) != io.EOF || out.SchemaVersion != 1 || out.DownloaderID != request.DownloaderID || out.Torrents == nil || out.Error && len(out.Torrents) != 0 {
		return TransmissionQueryResult{}, errors.New("invalid transmission query response")
	}
	for _, task := range out.Torrents {
		if task.Fields == nil {
			return TransmissionQueryResult{}, errors.New("invalid transmission task fields")
		}
	}
	return out, nil
}

func transmissionQueryUnicode(v reflect.Value, depth int) bool {
	if depth > 64 {
		return false
	}
	if !v.IsValid() {
		return true
	}
	switch v.Kind() {
	case reflect.String:
		return utf8.ValidString(v.String())
	case reflect.Interface, reflect.Pointer:
		return v.IsNil() || transmissionQueryUnicode(v.Elem(), depth+1)
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if !transmissionQueryUnicode(v.Index(i), depth+1) {
				return false
			}
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if !transmissionQueryUnicode(v.Field(i), depth+1) {
				return false
			}
		}
	case reflect.Map:
		return false // None of the finite source unions accepts an object.
	}
	return true
}
