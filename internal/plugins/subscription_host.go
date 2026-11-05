package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
)

const (
	SubscriptionsReadCapability   = "subscriptions.read"
	SubscriptionsUpdateCapability = "subscriptions.update"
)

// SubscriptionHostRequestV1 uses stable host IDs. Empty ID reads the complete
// matching list; a nonempty missing ID yields an empty list, like SubscribeOper.
type SubscriptionHostRequestV1 struct {
	ID     string `json:"id,omitempty"`
	State  string `json:"state,omitempty"`
	TMDBID *int64 `json:"tmdb_id,omitempty"`
	Season *int   `json:"season,omitempty"`
}

type SubscriptionHostResultV1 struct {
	SchemaVersion int               `json:"schema_version"`
	Items         []json.RawMessage `json:"items"`
}

// Patches preserve omitted fields. Site IDs are the host's stable IDs, not
// names. Columns map to existing native subscription behavior; identity and
// unsupported business columns are not arbitrary JSON storage.
type SubscriptionHostPatchV1 struct {
	// NullFields is limited to the ordinary nullable columns below. It explicitly
	// transmits field:null; omitted pointer fields remain omitted.
	NullFields         []string                `json:"-"`
	Sites              *[]string               `json:"sites,omitempty"`
	FilterGroups       *[]string               `json:"filter_groups,omitempty"`
	Resolution         *string                 `json:"resolution,omitempty"`
	Quality            *string                 `json:"quality,omitempty"`
	Effect             *string                 `json:"effect,omitempty"`
	Include            *string                 `json:"include,omitempty"`
	Exclude            *string                 `json:"exclude,omitempty"`
	SavePath           *string                 `json:"save_path,omitempty"`
	Keyword            *string                 `json:"keyword,omitempty"`
	IMDbID             *string                 `json:"imdbid,omitempty"`
	Name               *string                 `json:"name,omitempty"`
	Year               *string                 `json:"year,omitempty"`
	Poster             *string                 `json:"poster,omitempty"`
	Backdrop           *string                 `json:"backdrop,omitempty"`
	Vote               *float64                `json:"vote,omitempty"`
	Description        *string                 `json:"description,omitempty"`
	State              *string                 `json:"state,omitempty"`
	Season             *int                    `json:"season,omitempty"`
	TotalEpisode       *int                    `json:"total_episode,omitempty"`
	StartEpisode       *int                    `json:"start_episode,omitempty"`
	LackEpisode        *int                    `json:"lack_episode,omitempty"`
	Note               *[]int                  `json:"note,omitempty"`
	Downloader         *string                 `json:"downloader,omitempty"`
	BestVersion        *SubscriptionHostFlagV1 `json:"best_version,omitempty"`
	BestVersionFull    *SubscriptionHostFlagV1 `json:"best_version_full,omitempty"`
	CurrentPriority    *int                    `json:"current_priority,omitempty"`
	EpisodePriority    *map[string]int         `json:"episode_priority,omitempty"`
	SearchIMDbID       *SubscriptionHostFlagV1 `json:"search_imdbid,omitempty"`
	ManualTotalEpisode *SubscriptionHostFlagV1 `json:"manual_total_episode,omitempty"`
	CustomWords        *string                 `json:"custom_words,omitempty"`
}

// Four source flag columns permit SQLite numeric/text scalars. The host uses
// the persisted scalar's truth for native execution, preserving its projection.
// Exactly one member is set; SQL/JSON null is represented by Patch.NullFields.
type SubscriptionHostFlagV1 struct {
	Integer *int64
	Real    *float64
	Text    *string
}

func (f SubscriptionHostFlagV1) MarshalJSON() ([]byte, error) {
	n := 0
	var value any
	if f.Integer != nil {
		n++
		value = *f.Integer
	}
	if f.Real != nil {
		n++
		value = *f.Real
	}
	if f.Text != nil {
		n++
		value = *f.Text
	}
	if n != 1 {
		return nil, errors.New("source flag requires one scalar")
	}
	if f.Real != nil {
		if math.IsInf(*f.Real, 0) || math.IsNaN(*f.Real) {
			return nil, errors.New("source flag real out of JSON range")
		}
		// Keep a REAL scalar distinguishable from a Python integer bind when
		// Go's shortest formatter would otherwise emit an integral >int64 token.
		return strconv.AppendFloat(nil, *f.Real, 'e', -1, 64), nil
	}
	return json.Marshal(value)
}

func (f *SubscriptionHostFlagV1) UnmarshalJSON(raw []byte) error {
	if bytes.Equal(raw, []byte(`true`)) {
		v := int64(1)
		*f = SubscriptionHostFlagV1{Integer: &v}
		return nil
	}
	if bytes.Equal(raw, []byte(`false`)) {
		v := int64(0)
		*f = SubscriptionHostFlagV1{Integer: &v}
		return nil
	}
	if len(raw) > 0 && raw[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return err
		}
		*f = SubscriptionHostFlagV1{Text: &text}
		return nil
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return err
	}
	if strings.ContainsAny(string(number), ".eE") {
		v, err := strconv.ParseFloat(string(number), 64)
		if err != nil || math.IsInf(v, 0) || math.IsNaN(v) {
			return errors.New("source flag real out of range")
		}
		*f = SubscriptionHostFlagV1{Real: &v}
	} else {
		v, err := strconv.ParseInt(string(number), 10, 64)
		if err != nil {
			return errors.New("source flag integer out of SQLite range")
		}
		*f = SubscriptionHostFlagV1{Integer: &v}
	}
	return nil
}

var subscriptionHostNullableFields = []string{"sites", "filter_groups", "resolution", "quality", "effect", "include", "exclude", "save_path", "keyword", "imdbid", "year", "poster", "backdrop", "vote", "description", "season", "total_episode", "start_episode", "lack_episode", "note", "downloader", "best_version", "best_version_full", "current_priority", "episode_priority", "search_imdbid", "manual_total_episode", "custom_words"}

func subscriptionHostNullableField(key string) bool {
	for _, known := range subscriptionHostNullableFields {
		if key == known {
			return true
		}
	}
	return false
}

func (p SubscriptionHostPatchV1) MarshalJSON() ([]byte, error) {
	type plain SubscriptionHostPatchV1
	raw, err := json.Marshal(plain(p))
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	for _, key := range p.NullFields {
		if !subscriptionHostNullableField(key) {
			return nil, errors.New("unsupported nullable subscription field")
		}
		fields[key] = json.RawMessage(`null`)
	}
	return json.Marshal(fields)
}

func (p *SubscriptionHostPatchV1) UnmarshalJSON(raw []byte) error {
	type plain SubscriptionHostPatchV1
	var value plain
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&value); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("invalid trailing subscription patch")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return errors.New("subscription patch must be an object")
	}
	for _, key := range []string{"name", "state"} {
		if bytes.Equal(bytes.TrimSpace(fields[key]), []byte(`null`)) {
			return errors.New("non-nullable subscription column")
		}
	}
	for _, key := range subscriptionHostNullableFields {
		if bytes.Equal(bytes.TrimSpace(fields[key]), []byte(`null`)) {
			value.NullFields = append(value.NullFields, key)
		}
	}
	*p = SubscriptionHostPatchV1(value)
	return nil
}

type SubscriptionHostUpdateV1 struct {
	ID       string                  `json:"id"`
	Revision int                     `json:"revision,omitempty"`
	Patch    SubscriptionHostPatchV1 `json:"patch"`
}

type Subscriptions struct{ Host HostCaller }

func (s Subscriptions) Read(ctx context.Context, request SubscriptionHostRequestV1) (SubscriptionHostResultV1, error) {
	var out SubscriptionHostResultV1
	err := hostBusinessCall(ctx, s.Host, SubscriptionsReadCapability, request, &out)
	if err == nil && (out.SchemaVersion != 1 || out.Items == nil) {
		err = errors.New("invalid subscription response")
	}
	return out, err
}

func (s Subscriptions) Update(ctx context.Context, request SubscriptionHostUpdateV1) (SubscriptionHostResultV1, error) {
	var out SubscriptionHostResultV1
	err := hostBusinessCall(ctx, s.Host, SubscriptionsUpdateCapability, request, &out)
	if err == nil && (out.SchemaVersion != 1 || out.Items == nil || len(out.Items) > 1) {
		err = errors.New("invalid subscription response")
	}
	return out, err
}

func hostBusinessCall(ctx context.Context, host HostCaller, method string, request, result any) error {
	if host == nil {
		return errors.New("host resource requires an active host caller")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return err
	}
	raw, err = host(ctx, method, raw)
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if len(raw) > MaxPayloadBytes {
		return errors.New("host resource response too large")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err = d.Decode(result); err != nil {
		return err
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return errors.New("invalid trailing host response")
	}
	return nil
}
